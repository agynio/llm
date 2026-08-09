package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const attachmentColumns = `id, organization_id, subscription_id, vendor, agent_id, environment_id, created_at`

func scanAttachment(row pgx.Row) (Attachment, error) {
	var a Attachment
	var vendor string
	if err := row.Scan(&a.ID, &a.OrganizationID, &a.SubscriptionID, &vendor, &a.AgentID, &a.EnvironmentID, &a.CreatedAt); err != nil {
		return Attachment{}, err
	}
	a.Vendor = Vendor(vendor)
	return a, nil
}

// Attach denormalizes the subscription's vendor onto the row so the
// (vendor, target) uniqueness indexes can enforce it directly.
func (s *Store) Attach(ctx context.Context, input AttachInput) (Attachment, error) {
	sub, err := s.Get(ctx, input.SubscriptionID)
	if err != nil {
		return Attachment{}, err
	}
	row := s.pool.QueryRow(ctx,
		`INSERT INTO subscription_attachments (organization_id, subscription_id, vendor, agent_id, environment_id)
		 VALUES ($1, $2, $3, $4, $5) RETURNING `+attachmentColumns,
		input.OrganizationID, input.SubscriptionID, string(sub.Vendor), input.AgentID, input.EnvironmentID)
	attachment, err := scanAttachment(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Attachment{}, ErrVendorAlreadyBound
		}
		return Attachment{}, fmt.Errorf("insert subscription attachment: %w", err)
	}
	return attachment, nil
}

func (s *Store) Detach(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM subscription_attachments WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete subscription attachment: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAttachmentNotFound
	}
	return nil
}

func (s *Store) ListAttachments(ctx context.Context, filter AttachmentFilter, pageSize int32, cursor *PageCursor) (AttachmentListResult, error) {
	limit := normalizePageSize(pageSize)

	query := strings.Builder{}
	query.WriteString(`SELECT ` + attachmentColumns + ` FROM subscription_attachments WHERE organization_id = $1`)
	args := []any{filter.OrganizationID}
	if filter.SubscriptionID != nil {
		args = append(args, *filter.SubscriptionID)
		query.WriteString(fmt.Sprintf(" AND subscription_id = $%d", len(args)))
	}
	if filter.AgentID != nil {
		args = append(args, *filter.AgentID)
		query.WriteString(fmt.Sprintf(" AND agent_id = $%d", len(args)))
	}
	if filter.EnvironmentID != nil {
		args = append(args, *filter.EnvironmentID)
		query.WriteString(fmt.Sprintf(" AND environment_id = $%d", len(args)))
	}
	if cursor != nil {
		args = append(args, cursor.CreatedAt, cursor.ID)
		query.WriteString(fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	args = append(args, int(limit)+1)
	query.WriteString(fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT $%d", len(args)))

	rows, err := s.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return AttachmentListResult{}, fmt.Errorf("list subscription attachments: %w", err)
	}
	defer rows.Close()

	attachments := make([]Attachment, 0, limit)
	var nextCursor *PageCursor
	var last Attachment
	hasMore := false
	for rows.Next() {
		attachment, err := scanAttachment(rows)
		if err != nil {
			return AttachmentListResult{}, fmt.Errorf("scan subscription attachment: %w", err)
		}
		if int32(len(attachments)) == limit {
			hasMore = true
			break
		}
		attachments = append(attachments, attachment)
		last = attachment
	}
	if err := rows.Err(); err != nil {
		return AttachmentListResult{}, fmt.Errorf("list subscription attachments: %w", err)
	}
	if hasMore {
		nextCursor = &PageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return AttachmentListResult{Attachments: attachments, NextCursor: nextCursor}, nil
}

// Resolve answers the native-mode question: given the workload that is calling
// and the vendor whose host it addressed, which credential does it use? The
// agent scope is consulted first and shadows the environment's for the same
// vendor. Because attachments are unique on (vendor, target), at most one
// candidate exists at each scope and there is no ambiguity to resolve.
func (s *Store) Resolve(ctx context.Context, agentID *uuid.UUID, environmentID uuid.UUID, vendor Vendor) (Subscription, error) {
	if agentID != nil {
		sub, err := s.resolveAtScope(ctx, "agent_id", *agentID, vendor)
		if err == nil {
			return sub, nil
		}
		if !errors.Is(err, ErrSubscriptionNotFound) {
			return Subscription{}, err
		}
	}
	return s.resolveAtScope(ctx, "environment_id", environmentID, vendor)
}

func (s *Store) resolveAtScope(ctx context.Context, column string, target uuid.UUID, vendor Vendor) (Subscription, error) {
	row := s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s
		FROM subscriptions s
		JOIN subscription_attachments a ON a.subscription_id = s.id
		WHERE a.%s = $1 AND a.vendor = $2`,
		prefixColumns(subscriptionColumns, "s"), column), target, string(vendor))
	sub, err := scanSubscription(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, ErrSubscriptionNotFound
		}
		return Subscription{}, fmt.Errorf("resolve subscription: %w", err)
	}
	return sub, nil
}

func prefixColumns(columns, alias string) string {
	parts := strings.Split(columns, ", ")
	for i, part := range parts {
		parts[i] = alias + "." + part
	}
	return strings.Join(parts, ", ")
}

// ResolvedVendors lists the vendors a workload has a subscription for, at
// either scope. The Agents Orchestrator uses it to stamp role attributes and to
// inject each vendor's placeholder credential.
func (s *Store) ResolvedVendors(ctx context.Context, agentID *uuid.UUID, environmentID uuid.UUID) ([]Vendor, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT vendor FROM subscription_attachments
		WHERE environment_id = $1 OR ($2::uuid IS NOT NULL AND agent_id = $2)
		ORDER BY vendor`, environmentID, agentID)
	if err != nil {
		return nil, fmt.Errorf("list resolved vendors: %w", err)
	}
	defer rows.Close()
	var vendors []Vendor
	for rows.Next() {
		var vendor string
		if err := rows.Scan(&vendor); err != nil {
			return nil, fmt.Errorf("scan vendor: %w", err)
		}
		vendors = append(vendors, Vendor(vendor))
	}
	return vendors, rows.Err()
}

func (s *Store) GetAttachment(ctx context.Context, id uuid.UUID) (Attachment, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+attachmentColumns+` FROM subscription_attachments WHERE id = $1`, id)
	attachment, err := scanAttachment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Attachment{}, ErrAttachmentNotFound
		}
		return Attachment{}, fmt.Errorf("get subscription attachment: %w", err)
	}
	return attachment, nil
}
