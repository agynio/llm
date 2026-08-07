package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultGRPCAddress          = ":50051"
	defaultAuthorizationAddress = "authorization:50051"
	defaultSecretsAddress       = "secrets:50051"
	defaultAgentsAddress        = "agents:50051"
	defaultNotificationsAddress = "notifications:50051"
)

type Config struct {
	GRPCAddress          string
	AuthorizationAddress string
	DatabaseURL          string
	// Subscriptions hold their token by reference, resolve an environment's
	// model allowlist, and publish invalidation events, so native mode needs
	// all three.
	SecretsAddress       string
	AgentsAddress        string
	NotificationsAddress string
}

func FromEnv() (Config, error) {
	cfg := Config{}

	cfg.GRPCAddress = strings.TrimSpace(os.Getenv("GRPC_ADDRESS"))
	if cfg.GRPCAddress == "" {
		cfg.GRPCAddress = defaultGRPCAddress
	}
	cfg.AuthorizationAddress = strings.TrimSpace(os.Getenv("AUTHORIZATION_ADDRESS"))
	if cfg.AuthorizationAddress == "" {
		cfg.AuthorizationAddress = defaultAuthorizationAddress
	}
	cfg.SecretsAddress = envOrDefault("SECRETS_ADDRESS", defaultSecretsAddress)
	cfg.AgentsAddress = envOrDefault("AGENTS_ADDRESS", defaultAgentsAddress)
	cfg.NotificationsAddress = envOrDefault("NOTIFICATIONS_ADDRESS", defaultNotificationsAddress)
	var err error
	cfg.DatabaseURL, err = requiredEnv("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s must be set", name)
	}
	return value, nil
}
