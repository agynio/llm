//go:build e2e

package e2e

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
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
	conn, err := net.DialTimeout("tcp", llmAddress, 5*time.Second)
	if err != nil {
		t.Fatalf("connect to %s: %v", llmAddress, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}
}
