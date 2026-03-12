package model

import (
	"github.com/agynio/llm/internal/pagination"
	"github.com/google/uuid"
)

type PageCursor = pagination.PageCursor

func normalizePageSize(size int32) int32 {
	return pagination.NormalizePageSize(size)
}

func EncodePageToken(cursor PageCursor, providerID *uuid.UUID) (string, error) {
	return pagination.EncodePageToken(cursor, providerID)
}

func DecodePageToken(token string) (PageCursor, *uuid.UUID, error) {
	return pagination.DecodePageToken(token)
}
