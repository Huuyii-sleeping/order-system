package config

import (
	"fmt"
	"os"
	"strconv"
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

	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	SecureCookies    bool
	AuthRateWindow   time.Duration
	LoginRateLimit   int
	RefreshRateLimit int
	BcryptCost       int
}

func Load() (Config, error) {
	cfg := Config{
		AppName:          envOrDefault("APP_NAME", "order-system"),
		HTTPAddr:         envOrDefault("HTTP_ADDR", ":8080"),
		ShutdownTimeout:  10 * time.Second,
		StorageDriver:    strings.ToLower(envOrDefault("STORAGE_DRIVER", StorageMemory)),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		AccessTokenTTL:   15 * time.Minute,
		RefreshTokenTTL:  30 * 24 * time.Hour,
		SecureCookies:    false,
		AuthRateWindow:   time.Minute,
		LoginRateLimit:   10,
		RefreshRateLimit: 30,
		BcryptCost:       12,
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
		{name: "AUTH_RATE_WINDOW", target: &cfg.AuthRateWindow},
	} {
		if err := loadPositiveDuration(setting.name, setting.target); err != nil {
			return Config{}, err
		}
	}

	secureCookies, err := loadBool("SECURE_COOKIES", cfg.SecureCookies)
	if err != nil {
		return Config{}, err
	}
	cfg.SecureCookies = secureCookies
	loginRateLimit, err := loadPositiveInt("LOGIN_RATE_LIMIT", cfg.LoginRateLimit)
	if err != nil {
		return Config{}, err
	}
	cfg.LoginRateLimit = loginRateLimit
	refreshRateLimit, err := loadPositiveInt("REFRESH_RATE_LIMIT", cfg.RefreshRateLimit)
	if err != nil {
		return Config{}, err
	}
	cfg.RefreshRateLimit = refreshRateLimit

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

func loadBool(name string, fallback bool) (bool, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("parse %s %q: %w", name, raw, err)
	}
	return value, nil
}

func loadPositiveInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, raw, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s %q must be greater than zero", name, raw)
	}
	return value, nil
}

func envOrDefault(name string, fallback string) string {
	value := os.Getenv(name)

	if value != "" {
		return value
	}
	return fallback
}
