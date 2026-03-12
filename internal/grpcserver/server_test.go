package grpcserver

import (
	"errors"
	"testing"

	"github.com/agynio/llm/internal/model"
	"github.com/agynio/llm/internal/provider"
	"github.com/agynio/llm/internal/proxy"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatusErrorMappings(t *testing.T) {
	statusErr := status.Error(codes.NotFound, "already status")
	cases := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "status error passthrough", err: statusErr, code: codes.NotFound},
		{name: "provider not found", err: provider.ErrProviderNotFound, code: codes.NotFound},
		{name: "model not found", err: model.ErrModelNotFound, code: codes.NotFound},
		{name: "provider in use", err: provider.ErrProviderInUse, code: codes.FailedPrecondition},
		{name: "no provider fields", err: provider.ErrNoFieldsToUpdate, code: codes.InvalidArgument},
		{name: "no model fields", err: model.ErrNoFieldsToUpdate, code: codes.InvalidArgument},
		{name: "invalid body", err: proxy.ErrInvalidBody, code: codes.InvalidArgument},
		{name: "missing model", err: proxy.ErrMissingModel, code: codes.InvalidArgument},
		{name: "unsupported auth", err: proxy.ErrUnsupportedAuthMethod, code: codes.FailedPrecondition},
		{name: "missing token", err: proxy.ErrMissingToken, code: codes.FailedPrecondition},
		{name: "fallback", err: errors.New("boom"), code: codes.Internal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := toStatusError(tc.err)
			if status.Code(err) != tc.code {
				t.Fatalf("expected code %v, got %v", tc.code, status.Code(err))
			}
		})
	}
}
