package config

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultGRPCAddress = ":50051"
	defaultHTTPAddress = ":8080"
)

type Config struct {
	GRPCAddress string
	HTTPAddress string
	DatabaseURL string
}

func FromEnv() (Config, error) {
	cfg := Config{}

	cfg.GRPCAddress = strings.TrimSpace(os.Getenv("GRPC_ADDRESS"))
	if cfg.GRPCAddress == "" {
		cfg.GRPCAddress = defaultGRPCAddress
	}
	cfg.HTTPAddress = strings.TrimSpace(os.Getenv("HTTP_ADDRESS"))
	if cfg.HTTPAddress == "" {
		cfg.HTTPAddress = defaultHTTPAddress
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
