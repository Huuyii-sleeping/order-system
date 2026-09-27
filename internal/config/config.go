package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	StorageMemory   = "memory"
	StoragePostgres = "postgres"
)

type Config struct {
	AppName string

	HTTPAddr string

	ShutdownTimeout time.Duration

	StorageDriver string
	DatabaseURL   string
}

func Load() (Config, error) {
	cfg := Config{
		AppName:         envOrDefault("APP_NAME", "order-system"),
		HTTPAddr:        envOrDefault("HTTP_ADDR", ":8080"),
		ShutdownTimeout: 10 * time.Second,
		StorageDriver:   strings.ToLower(envOrDefault("STORAGE_DRIVER", StorageMemory)),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
	}

	switch cfg.StorageDriver {
	case StorageMemory:
	case StoragePostgres:
		if strings.TrimSpace(cfg.DatabaseURL) == "" {
			return Config{}, fmt.Errorf("DATABASE_URL is required when STORAGE_DRIVER is %q", StoragePostgres)
		}
	default:
		return Config{}, fmt.Errorf("unsupported STORAGE_DRIVER %q", cfg.StorageDriver)
	}

	rawShutdownTimeout := os.Getenv("SHUTDOWN_TIMEOUT")

	if rawShutdownTimeout == "" {
		return cfg, nil
	}

	shutdownTimeout, err := time.ParseDuration(rawShutdownTimeout)
	if err != nil {
		return Config{}, fmt.Errorf(
			"parse SHUTDOWN_TIMEOUT %q: %w",
			rawShutdownTimeout,
			err,
		)
	}

	if shutdownTimeout <= 0 {
		return Config{}, fmt.Errorf(
			"SHUTDOWN_TIMEOUT %q must be greater than zero",
			rawShutdownTimeout,
		)
	}

	cfg.ShutdownTimeout = shutdownTimeout

	return cfg, nil
}

func envOrDefault(name string, fallback string) string {
	value := os.Getenv(name)

	if value != "" {
		return value
	}
	return fallback
}
