package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	defaultPageSize int32 = 50
	maxPageSize     int32 = 100
)

type PageCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
}

func NormalizePageSize(size int32) int32 {
	if size <= 0 {
		return defaultPageSize
	}
	if size > maxPageSize {
		return maxPageSize
	}
	return size
}

type pageToken struct {
	CreatedAt  time.Time `json:"created_at"`
	ID         string    `json:"id"`
	ProviderID string    `json:"provider_id,omitempty"`
}

func EncodePageToken(cursor PageCursor, providerID *uuid.UUID) (string, error) {
	payload := pageToken{CreatedAt: cursor.CreatedAt, ID: cursor.ID.String()}
	if providerID != nil {
		payload.ProviderID = providerID.String()
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func DecodePageToken(token string) (PageCursor, *uuid.UUID, error) {
	if token == "" {
		return PageCursor{}, nil, errors.New("empty token")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return PageCursor{}, nil, fmt.Errorf("decode token: %w", err)
	}
	var payload pageToken
	if err := json.Unmarshal(data, &payload); err != nil {
		return PageCursor{}, nil, fmt.Errorf("unmarshal token: %w", err)
	}
	id, err := uuid.Parse(payload.ID)
	if err != nil {
		return PageCursor{}, nil, fmt.Errorf("parse id: %w", err)
	}
	var providerID *uuid.UUID
	if payload.ProviderID != "" {
		parsed, err := uuid.Parse(payload.ProviderID)
		if err != nil {
			return PageCursor{}, nil, fmt.Errorf("parse provider id: %w", err)
		}
		providerID = &parsed
	}
	return PageCursor{CreatedAt: payload.CreatedAt, ID: id}, providerID, nil
}
