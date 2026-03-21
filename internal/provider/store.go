package provider

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

type AuthMethod string

const (
	AuthMethodBearer AuthMethod = "bearer"
)

var (
	ErrProviderNotFound = errors.New("provider not found")
	ErrProviderInUse    = errors.New("provider in use")
	ErrNoFieldsToUpdate = errors.New("no fields to update")
)

type Provider struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Endpoint   string
	AuthMethod AuthMethod
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ProviderWithToken struct {
	Provider
	Token string
}

type CreateInput struct {
	TenantID   uuid.UUID
	Endpoint   string
	AuthMethod AuthMethod
	Token      string
}

type UpdateInput struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Endpoint   *string
	AuthMethod *AuthMethod
	Token      *string
}

type ListResult struct {
	Providers  []Provider
	NextCursor *PageCursor
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Create(ctx context.Context, tenantID uuid.UUID, input CreateInput) (Provider, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO llm_providers (tenant_id, endpoint, auth_method, token) VALUES ($1, $2, $3, $4) RETURNING id, tenant_id, endpoint, auth_method, created_at, updated_at`, tenantID, input.Endpoint, string(input.AuthMethod), input.Token)
	var provider Provider
	var authMethod string
	if err := row.Scan(&provider.ID, &provider.TenantID, &provider.Endpoint, &authMethod, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
		return Provider{}, fmt.Errorf("insert provider: %w", err)
	}
	provider.AuthMethod = AuthMethod(authMethod)
	return provider, nil
}

func (s *Store) Get(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) (Provider, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, tenant_id, endpoint, auth_method, created_at, updated_at FROM llm_providers WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	var provider Provider
	var authMethod string
	if err := row.Scan(&provider.ID, &provider.TenantID, &provider.Endpoint, &authMethod, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Provider{}, ErrProviderNotFound
		}
		return Provider{}, fmt.Errorf("get provider: %w", err)
	}
	provider.AuthMethod = AuthMethod(authMethod)
	return provider, nil
}

func (s *Store) GetWithToken(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) (ProviderWithToken, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, tenant_id, endpoint, auth_method, token, created_at, updated_at FROM llm_providers WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	var provider ProviderWithToken
	var authMethod string
	if err := row.Scan(&provider.ID, &provider.TenantID, &provider.Endpoint, &authMethod, &provider.Token, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProviderWithToken{}, ErrProviderNotFound
		}
		return ProviderWithToken{}, fmt.Errorf("get provider: %w", err)
	}
	provider.AuthMethod = AuthMethod(authMethod)
	return provider, nil
}
func (s *Store) Update(ctx context.Context, tenantID uuid.UUID, input UpdateInput) (Provider, error) {
	setClauses := make([]string, 0, 3)
	args := make([]any, 0, 5)

	if input.Endpoint != nil {
		setClauses = append(setClauses, fmt.Sprintf("endpoint = $%d", len(args)+1))
		args = append(args, *input.Endpoint)
	}
	if input.AuthMethod != nil {
		setClauses = append(setClauses, fmt.Sprintf("auth_method = $%d", len(args)+1))
		args = append(args, string(*input.AuthMethod))
	}
	if input.Token != nil {
		setClauses = append(setClauses, fmt.Sprintf("token = $%d", len(args)+1))
		args = append(args, *input.Token)
	}
	if len(setClauses) == 0 {
		return Provider{}, ErrNoFieldsToUpdate
	}
	setClauses = append(setClauses, "updated_at = NOW()")
	idIndex := len(args) + 1
	tenantIndex := len(args) + 2
	args = append(args, input.ID, tenantID)

	query := fmt.Sprintf("UPDATE llm_providers SET %s WHERE id = $%d AND tenant_id = $%d RETURNING id, tenant_id, endpoint, auth_method, created_at, updated_at", strings.Join(setClauses, ", "), idIndex, tenantIndex)
	row := s.pool.QueryRow(ctx, query, args...)

	var provider Provider
	var authMethod string
	if err := row.Scan(&provider.ID, &provider.TenantID, &provider.Endpoint, &authMethod, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Provider{}, ErrProviderNotFound
		}
		return Provider{}, fmt.Errorf("update provider: %w", err)
	}
	provider.AuthMethod = AuthMethod(authMethod)
	return provider, nil
}

func (s *Store) Delete(ctx context.Context, tenantID uuid.UUID, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM llm_providers WHERE id = $1 AND tenant_id = $2`, id, tenantID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return ErrProviderInUse
		}
		return fmt.Errorf("delete provider: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrProviderNotFound
	}
	return nil
}

func (s *Store) List(ctx context.Context, tenantID uuid.UUID, pageSize int32, cursor *PageCursor) (ListResult, error) {
	limit := normalizePageSize(pageSize)

	query := strings.Builder{}
	query.WriteString(`SELECT id, tenant_id, endpoint, auth_method, created_at, updated_at FROM llm_providers WHERE tenant_id = $1`)
	args := []any{tenantID}
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
		return ListResult{}, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	providers := make([]Provider, 0, limit)
	var (
		nextCursor *PageCursor
		last       Provider
		hasMore    bool
	)
	for rows.Next() {
		var provider Provider
		var authMethod string
		if err := rows.Scan(&provider.ID, &provider.TenantID, &provider.Endpoint, &authMethod, &provider.CreatedAt, &provider.UpdatedAt); err != nil {
			return ListResult{}, fmt.Errorf("scan provider: %w", err)
		}
		if int32(len(providers)) == limit {
			hasMore = true
			break
		}
		provider.AuthMethod = AuthMethod(authMethod)
		providers = append(providers, provider)
		last = provider
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate providers: %w", err)
	}
	if hasMore {
		nextCursor = &PageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return ListResult{Providers: providers, NextCursor: nextCursor}, nil
}
