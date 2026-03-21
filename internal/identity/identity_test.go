package identity

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestFromContext(t *testing.T) {
	identityID := "user-123"

	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		MetadataKeyIdentityID, identityID,
		MetadataKeyIdentityType, "user",
	))

	ident, err := FromContext(ctx)
	if err != nil {
		t.Fatalf("FromContext: %v", err)
	}
	if ident.IdentityID != identityID {
		t.Fatalf("expected identity id %s, got %s", identityID, ident.IdentityID)
	}
	if ident.IdentityType != "user" {
		t.Fatalf("expected identity type user, got %s", ident.IdentityType)
	}
}

func TestFromContextMissingMetadata(t *testing.T) {
	_, err := FromContext(context.Background())
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected unauthenticated, got %v", status.Code(err))
	}
}

func TestFromContextMissingFields(t *testing.T) {
	identityID := "svc-42"

	cases := []struct {
		name string
		key  string
	}{
		{name: "identity id", key: MetadataKeyIdentityID},
		{name: "identity type", key: MetadataKeyIdentityType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			md := metadata.Pairs(
				MetadataKeyIdentityID, identityID,
				MetadataKeyIdentityType, "service",
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
