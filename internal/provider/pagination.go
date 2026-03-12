package provider

import (
	"errors"

	"github.com/agynio/llm/internal/pagination"
)

type PageCursor = pagination.PageCursor

func normalizePageSize(size int32) int32 {
	return pagination.NormalizePageSize(size)
}

func EncodePageToken(cursor PageCursor) (string, error) {
	return pagination.EncodePageToken(cursor, nil)
}

func DecodePageToken(token string) (PageCursor, error) {
	cursor, providerID, err := pagination.DecodePageToken(token)
	if err != nil {
		return PageCursor{}, err
	}
	if providerID != nil {
		return PageCursor{}, errors.New("unexpected provider id")
	}
	return cursor, nil
}
