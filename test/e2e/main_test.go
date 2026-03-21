//go:build e2e

package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/agynio/llm/.gen/go/agynio/api/llm/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var llmAddress = envOrDefault("LLM_ADDRESS", "llm:50051")

func envOrDefault(key, fallback string) string {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestGRPCConnectivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, llmAddress, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("connect to %s: %v", llmAddress, err)
	}
	defer conn.Close()

	client := llmv1.NewLLMServiceClient(conn)
	md := metadata.Pairs(
		"x-agyn-tenant-id", uuid.MustParse("c0879bc3-996c-45ac-9b7d-5535f891a0e3").String(),
		"x-agyn-identity-id", uuid.MustParse("85e1e2b5-2425-4b55-a2f4-42ca1d5d8e9b").String(),
		"x-agyn-identity-type", "e2e",
		"x-agyn-auth-method", "test",
	)
	ctx = metadata.NewOutgoingContext(ctx, md)

	if _, err := client.ListLLMProviders(ctx, &llmv1.ListLLMProvidersRequest{}); err != nil {
		t.Fatalf("list llm providers: %v", err)
	}
}
