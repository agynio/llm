package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Vendor is closed: each value fixes an intercepted host, an upstream, a
// protocol, a header set, and how the placeholder credential is delivered.
// Named after the API the credential authenticates rather than after a CLI
// that presents it -- which CLI does is incidental and free to change.
type Vendor string

const (
	VendorAnthropic Vendor = "anthropic"
	VendorOpenAI    Vendor = "openai"
)

var (
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrAttachmentNotFound   = errors.New("subscription attachment not found")
	ErrSubscriptionInUse    = errors.New("subscription in use")
	ErrNameTaken            = errors.New("subscription name taken")
	ErrVendorAlreadyBound   = errors.New("target already has a subscription for this vendor")
	ErrNoFieldsToUpdate     = errors.New("no fields to update")
)

type Subscription struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	Vendor         Vendor
	SecretID       uuid.UUID
	AccountID      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Attachment binds a subscription to exactly one of an agent or an environment.
type Attachment struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	SubscriptionID uuid.UUID
	Vendor         Vendor
	AgentID        *uuid.UUID
	EnvironmentID  *uuid.UUID
	CreatedAt      time.Time
}

type CreateInput struct {
	OrganizationID uuid.UUID
	Name           string
	Vendor         Vendor
	SecretID       uuid.UUID
	AccountID      string
}

// Vendor is absent: changing it would silently redirect every workload the
// subscription is attached to.
type UpdateInput struct {
	ID        uuid.UUID
	Name      *string
	SecretID  *uuid.UUID
	AccountID *string
}

type AttachInput struct {
	OrganizationID uuid.UUID
	SubscriptionID uuid.UUID
	AgentID        *uuid.UUID
	EnvironmentID  *uuid.UUID
}

type AttachmentFilter struct {
	OrganizationID uuid.UUID
	SubscriptionID *uuid.UUID
	AgentID        *uuid.UUID
	EnvironmentID  *uuid.UUID
}

type ListResult struct {
	Subscriptions []Subscription
	NextCursor    *PageCursor
}

type AttachmentListResult struct {
	Attachments []Attachment
	NextCursor  *PageCursor
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const subscriptionColumns = `id, organization_id, name, vendor, secret_id, account_id, created_at, updated_at`

func scanSubscription(row pgx.Row) (Subscription, error) {
	var s Subscription
	var vendor string
	if err := row.Scan(&s.ID, &s.OrganizationID, &s.Name, &vendor, &s.SecretID, &s.AccountID, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return Subscription{}, err
	}
	s.Vendor = Vendor(vendor)
	return s, nil
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Subscription, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO subscriptions (organization_id, name, vendor, secret_id, account_id)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+subscriptionColumns,
		input.OrganizationID, input.Name, string(input.Vendor), input.SecretID, input.AccountID)
	sub, err := scanSubscription(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Subscription{}, ErrNameTaken
		}
		return Subscription{}, fmt.Errorf("insert subscription: %w", err)
	}
	return sub, nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (Subscription, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE id = $1`, id)
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, ErrSubscriptionNotFound
		}
		return Subscription{}, fmt.Errorf("get subscription: %w", err)
	}
	return sub, nil
}

func (s *Store) Update(ctx context.Context, input UpdateInput) (Subscription, error) {
	setClauses := make([]string, 0, 3)
	args := make([]any, 0, 4)

	if input.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", len(args)+1))
		args = append(args, *input.Name)
	}
	if input.SecretID != nil {
		setClauses = append(setClauses, fmt.Sprintf("secret_id = $%d", len(args)+1))
		args = append(args, *input.SecretID)
	}
	if input.AccountID != nil {
		setClauses = append(setClauses, fmt.Sprintf("account_id = $%d", len(args)+1))
		args = append(args, *input.AccountID)
	}
	if len(setClauses) == 0 {
		return Subscription{}, ErrNoFieldsToUpdate
	}
	setClauses = append(setClauses, "updated_at = now()")
	args = append(args, input.ID)

	row := s.pool.QueryRow(ctx,
		fmt.Sprintf(`UPDATE subscriptions SET %s WHERE id = $%d RETURNING %s`,
			strings.Join(setClauses, ", "), len(args), subscriptionColumns),
		args...)
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, ErrSubscriptionNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Subscription{}, ErrNameTaken
		}
		return Subscription{}, fmt.Errorf("update subscription: %w", err)
	}
	return sub, nil
}

// Delete refuses while any attachment exists; the caller names them in the
// error by listing attachments first.
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM subscriptions WHERE id = $1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrSubscriptionInUse
		}
		return fmt.Errorf("delete subscription: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

func (s *Store) List(ctx context.Context, organizationID uuid.UUID, pageSize int32, cursor *PageCursor) (ListResult, error) {
	limit := normalizePageSize(pageSize)

	query := strings.Builder{}
	query.WriteString(`SELECT ` + subscriptionColumns + ` FROM subscriptions WHERE organization_id = $1`)
	args := []any{organizationID}
	paramIndex := 2
	if cursor != nil {
		query.WriteString(fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursor.CreatedAt, cursor.ID)
		paramIndex += 2
	}
	query.WriteString(fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT $%d", paramIndex))
	args = append(args, int(limit)+1)

	rows, err := s.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("list subscriptions: %w", err)
	}
	defer rows.Close()

	subs := make([]Subscription, 0, limit)
	var nextCursor *PageCursor
	var last Subscription
	hasMore := false
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return ListResult{}, fmt.Errorf("scan subscription: %w", err)
		}
		if int32(len(subs)) == limit {
			hasMore = true
			break
		}
		subs = append(subs, sub)
		last = sub
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("list subscriptions: %w", err)
	}
	if hasMore {
		nextCursor = &PageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return ListResult{Subscriptions: subs, NextCursor: nextCursor}, nil
}

// CountReferencingSecret backs the check Secrets makes before deleting a
// secret. It returns the ids so the refusal can name them.
func (s *Store) CountReferencingSecret(ctx context.Context, secretID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM subscriptions WHERE secret_id = $1 ORDER BY id`, secretID)
	if err != nil {
		return nil, fmt.Errorf("count subscriptions referencing secret: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan subscription id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
