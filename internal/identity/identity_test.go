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
	identityID := "user-123"

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		tenantIDKey, tenantID.String(),
		identityIDKey, identityID,
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
	identityID := "svc-42"

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
				identityIDKey, identityID,
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
	md := metadata.Pairs(
		tenantIDKey, "not-a-uuid",
		identityIDKey, "identity-99",
		identityTypeKey, "user",
		authMethodKey, "jwt",
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := FromContext(ctx)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v", status.Code(err))
	}
}
