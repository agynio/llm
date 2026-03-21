package identity

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	tenantIDKey     = "x-agyn-tenant-id"
	identityIDKey   = "x-agyn-identity-id"
	identityTypeKey = "x-agyn-identity-type"
	authMethodKey   = "x-agyn-auth-method"
)

type Identity struct {
	TenantID     uuid.UUID
	IdentityID   string
	IdentityType string
	AuthMethod   string
}

func FromContext(ctx context.Context) (Identity, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return Identity{}, status.Error(codes.Unauthenticated, "missing identity metadata")
	}

	tenantID, err := parseUUID(md, tenantIDKey, "tenant id")
	if err != nil {
		return Identity{}, err
	}
	identityID, err := parseString(md, identityIDKey, "identity id")
	if err != nil {
		return Identity{}, err
	}
	identityType, err := parseString(md, identityTypeKey, "identity type")
	if err != nil {
		return Identity{}, err
	}
	authMethod, err := parseString(md, authMethodKey, "auth method")
	if err != nil {
		return Identity{}, err
	}

	return Identity{
		TenantID:     tenantID,
		IdentityID:   identityID,
		IdentityType: identityType,
		AuthMethod:   authMethod,
	}, nil
}

func parseUUID(md metadata.MD, key string, label string) (uuid.UUID, error) {
	value, err := parseString(md, key, label)
	if err != nil {
		return uuid.Nil, err
	}
	parsed, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, status.Errorf(codes.Unauthenticated, "%s must be a valid UUID", label)
	}
	return parsed, nil
}

func parseString(md metadata.MD, key string, label string) (string, error) {
	values := md.Get(key)
	value := ""
	if len(values) > 0 {
		value = values[0]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", status.Errorf(codes.Unauthenticated, "%s is required", label)
	}
	return value, nil
}
