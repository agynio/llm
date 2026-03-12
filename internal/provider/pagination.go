package provider

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

func normalizePageSize(size int32) int32 {
	if size <= 0 {
		return defaultPageSize
	}
	if size > maxPageSize {
		return maxPageSize
	}
	return size
}

type pageToken struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func EncodePageToken(cursor PageCursor) (string, error) {
	payload := pageToken{CreatedAt: cursor.CreatedAt, ID: cursor.ID.String()}
	buf, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func DecodePageToken(token string) (PageCursor, error) {
	if token == "" {
		return PageCursor{}, errors.New("empty token")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return PageCursor{}, fmt.Errorf("decode token: %w", err)
	}
	var payload pageToken
	if err := json.Unmarshal(data, &payload); err != nil {
		return PageCursor{}, fmt.Errorf("unmarshal token: %w", err)
	}
	id, err := uuid.Parse(payload.ID)
	if err != nil {
		return PageCursor{}, fmt.Errorf("parse id: %w", err)
	}
	return PageCursor{CreatedAt: payload.CreatedAt, ID: id}, nil
}
