package identity

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	MetadataKeyIdentityID   = "x-identity-id"
	MetadataKeyIdentityType = "x-identity-type"
)

type Identity struct {
	IdentityID   string
	IdentityType string
}

func FromContext(ctx context.Context) (Identity, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return Identity{}, status.Error(codes.Unauthenticated, "missing identity metadata")
	}

	identityID, err := parseString(md, MetadataKeyIdentityID, "identity id")
	if err != nil {
		return Identity{}, err
	}
	identityType, err := parseString(md, MetadataKeyIdentityType, "identity type")
	if err != nil {
		return Identity{}, err
	}

	return Identity{
		IdentityID:   identityID,
		IdentityType: identityType,
	}, nil
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
