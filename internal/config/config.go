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

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	BcryptCost      int
}

func Load() (Config, error) {
	cfg := Config{
		AppName:         envOrDefault("APP_NAME", "order-system"),
		HTTPAddr:        envOrDefault("HTTP_ADDR", ":8080"),
		ShutdownTimeout: 10 * time.Second,
		StorageDriver:   strings.ToLower(envOrDefault("STORAGE_DRIVER", StorageMemory)),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		BcryptCost:      12,
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

	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{name: "SHUTDOWN_TIMEOUT", target: &cfg.ShutdownTimeout},
		{name: "ACCESS_TOKEN_TTL", target: &cfg.AccessTokenTTL},
		{name: "REFRESH_TOKEN_TTL", target: &cfg.RefreshTokenTTL},
	} {
		if err := loadPositiveDuration(setting.name, setting.target); err != nil {
			return Config{}, err
		}
	}

	return cfg, nil
}

func loadPositiveDuration(name string, target *time.Duration) error {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse %s %q: %w", name, raw, err)
	}
	if duration <= 0 {
		return fmt.Errorf("%s %q must be greater than zero", name, raw)
	}
	*target = duration
	return nil
}

func envOrDefault(name string, fallback string) string {
	value := os.Getenv(name)

	if value != "" {
		return value
	}
	return fallback
}
