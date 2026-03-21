package model

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrModelNotFound    = errors.New("model not found")
	ErrNoFieldsToUpdate = errors.New("no fields to update")
)

type Model struct {
	ID             uuid.UUID
	OrganizationID uuid.UUID
	Name           string
	ProviderID     uuid.UUID
	RemoteName     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateInput struct {
	OrganizationID uuid.UUID
	Name           string
	ProviderID     uuid.UUID
	RemoteName     string
}

type UpdateInput struct {
	ID         uuid.UUID
	Name       *string
	ProviderID *uuid.UUID
	RemoteName *string
}

type ListFilter struct {
	ProviderID *uuid.UUID
}

type ListResult struct {
	Models     []Model
	NextCursor *PageCursor
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Create(ctx context.Context, input CreateInput) (Model, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO models (organization_id, name, llm_provider_id, remote_name) VALUES ($1, $2, $3, $4) RETURNING id, organization_id, name, llm_provider_id, remote_name, created_at, updated_at`, input.OrganizationID, input.Name, input.ProviderID, input.RemoteName)
	var model Model
	if err := row.Scan(&model.ID, &model.OrganizationID, &model.Name, &model.ProviderID, &model.RemoteName, &model.CreatedAt, &model.UpdatedAt); err != nil {
		return Model{}, fmt.Errorf("insert model: %w", err)
	}
	return model, nil
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (Model, error) {
	row := s.pool.QueryRow(ctx, `SELECT id, organization_id, name, llm_provider_id, remote_name, created_at, updated_at FROM models WHERE id = $1`, id)
	var model Model
	if err := row.Scan(&model.ID, &model.OrganizationID, &model.Name, &model.ProviderID, &model.RemoteName, &model.CreatedAt, &model.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Model{}, ErrModelNotFound
		}
		return Model{}, fmt.Errorf("get model: %w", err)
	}
	return model, nil
}

func (s *Store) Update(ctx context.Context, input UpdateInput) (Model, error) {
	setClauses := make([]string, 0, 4)
	args := make([]any, 0, 5)

	if input.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", len(args)+1))
		args = append(args, *input.Name)
	}
	if input.ProviderID != nil {
		setClauses = append(setClauses, fmt.Sprintf("llm_provider_id = $%d", len(args)+1))
		args = append(args, *input.ProviderID)
	}
	if input.RemoteName != nil {
		setClauses = append(setClauses, fmt.Sprintf("remote_name = $%d", len(args)+1))
		args = append(args, *input.RemoteName)
	}
	if len(setClauses) == 0 {
		return Model{}, ErrNoFieldsToUpdate
	}
	setClauses = append(setClauses, "updated_at = NOW()")
	args = append(args, input.ID)

	query := fmt.Sprintf("UPDATE models SET %s WHERE id = $%d RETURNING id, organization_id, name, llm_provider_id, remote_name, created_at, updated_at", strings.Join(setClauses, ", "), len(args))
	row := s.pool.QueryRow(ctx, query, args...)

	var model Model
	if err := row.Scan(&model.ID, &model.OrganizationID, &model.Name, &model.ProviderID, &model.RemoteName, &model.CreatedAt, &model.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Model{}, ErrModelNotFound
		}
		return Model{}, fmt.Errorf("update model: %w", err)
	}
	return model, nil
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM models WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete model: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrModelNotFound
	}
	return nil
}

func (s *Store) List(ctx context.Context, organizationID uuid.UUID, filter ListFilter, pageSize int32, cursor *PageCursor) (ListResult, error) {
	limit := normalizePageSize(pageSize)

	query := strings.Builder{}
	query.WriteString(`SELECT id, organization_id, name, llm_provider_id, remote_name, created_at, updated_at FROM models WHERE organization_id = $1`)

	args := []any{organizationID}
	paramIndex := 2
	if filter.ProviderID != nil {
		query.WriteString(fmt.Sprintf(" AND llm_provider_id = $%d", paramIndex))
		args = append(args, *filter.ProviderID)
		paramIndex++
	}
	if cursor != nil {
		query.WriteString(fmt.Sprintf(" AND (created_at, id) > ($%d, $%d)", paramIndex, paramIndex+1))
		args = append(args, cursor.CreatedAt, cursor.ID)
		paramIndex += 2
	}

	query.WriteString(fmt.Sprintf(" ORDER BY created_at ASC, id ASC LIMIT $%d", paramIndex))
	args = append(args, int(limit)+1)

	rows, err := s.pool.Query(ctx, query.String(), args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()

	models := make([]Model, 0, limit)
	var (
		nextCursor *PageCursor
		last       Model
		hasMore    bool
	)
	for rows.Next() {
		var model Model
		if err := rows.Scan(&model.ID, &model.OrganizationID, &model.Name, &model.ProviderID, &model.RemoteName, &model.CreatedAt, &model.UpdatedAt); err != nil {
			return ListResult{}, fmt.Errorf("scan model: %w", err)
		}
		if int32(len(models)) == limit {
			hasMore = true
			break
		}
		models = append(models, model)
		last = model
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate models: %w", err)
	}
	if hasMore {
		nextCursor = &PageCursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return ListResult{Models: models, NextCursor: nextCursor}, nil
}
