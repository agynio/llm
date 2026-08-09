package subscription

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
	cursor, extra, err := pagination.DecodePageToken(token)
	if err != nil {
		return PageCursor{}, err
	}
	if extra != nil {
		return PageCursor{}, errors.New("unexpected id in page token")
	}
	return cursor, nil
}
