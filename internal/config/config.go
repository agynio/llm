package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultGRPCAddress = ":50051"
)

type Config struct {
	GRPCAddress string
	DatabaseURL string
}

func FromEnv() (Config, error) {
	cfg := Config{}

	cfg.GRPCAddress = strings.TrimSpace(os.Getenv("GRPC_ADDRESS"))
	if cfg.GRPCAddress == "" {
		cfg.GRPCAddress = defaultGRPCAddress
	}

	var err error
	cfg.DatabaseURL, err = requiredEnv("DATABASE_URL")
	if err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s must be set", name)
	}
	return value, nil
}
