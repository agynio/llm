package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestFromContext(t *testing.T) {
	tenantID := uuid.MustParse("75b094d1-4bba-4b85-9472-5e2eecb9f9d6")
	identityID := uuid.MustParse("e0b7a948-cdb7-4fd1-b691-ea6382d42cf4")

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		tenantIDKey, tenantID.String(),
		identityIDKey, identityID.String(),
		identityTypeKey, "user",
		authMethodKey, "jwt",
	))

	ident, err := FromContext(ctx)
	if err != nil {
		t.Fatalf("FromContext: %v", err)
	}
	if ident.TenantID != tenantID {
		t.Fatalf("expected tenant id %s, got %s", tenantID, ident.TenantID)
	}
	if ident.IdentityID != identityID {
		t.Fatalf("expected identity id %s, got %s", identityID, ident.IdentityID)
	}
	if ident.IdentityType != "user" {
		t.Fatalf("expected identity type user, got %s", ident.IdentityType)
	}
	if ident.AuthMethod != "jwt" {
		t.Fatalf("expected auth method jwt, got %s", ident.AuthMethod)
	}
}

func TestFromContextMissingMetadata(t *testing.T) {
	_, err := FromContext(context.Background())
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v", status.Code(err))
	}
}

func TestFromContextMissingFields(t *testing.T) {
	tenantID := uuid.MustParse("8882d0e1-24cb-4de0-9ab5-96cf8b8d4bf2")
	identityID := uuid.MustParse("e1f99b91-0c20-4db0-9e5c-3a6f8a9d4c2c")

	cases := []struct {
		name string
		key  string
	}{
		{name: "tenant id", key: tenantIDKey},
		{name: "identity id", key: identityIDKey},
		{name: "identity type", key: identityTypeKey},
		{name: "auth method", key: authMethodKey},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.Pairs(
				tenantIDKey, tenantID.String(),
				identityIDKey, identityID.String(),
				identityTypeKey, "service",
				authMethodKey, "token",
			)
			delete(md, tc.key)
			ctx := metadata.NewIncomingContext(context.Background(), md)

			_, err := FromContext(ctx)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("expected unauthenticated, got %v", status.Code(err))
			}
		})
	}
}

func TestFromContextInvalidUUID(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "tenant id", key: tenantIDKey, value: "not-a-uuid"},
		{name: "identity id", key: identityIDKey, value: "also-not"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.Pairs(
				tenantIDKey, "6900b04d-7a3f-46d2-8724-1f5ccfc3f3ce",
				identityIDKey, "86dd122e-9842-42e9-8b2c-92f78b4d8f1b",
				identityTypeKey, "user",
				authMethodKey, "jwt",
			)
			md[tc.key] = []string{tc.value}
			ctx := metadata.NewIncomingContext(context.Background(), md)

			_, err := FromContext(ctx)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("expected unauthenticated, got %v", status.Code(err))
			}
		})
	}
}
