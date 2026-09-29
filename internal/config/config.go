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

	ShutdownTimeout  time.Duration
	ReadinessTimeout time.Duration

	StorageDriver string
	DatabaseURL   string

	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration
	SecureCookies    bool
	AuthRateWindow   time.Duration
	LoginRateLimit   int
	RefreshRateLimit int
	BcryptCost       int

	OutboxPollInterval  time.Duration
	OutboxBatchSize     int
	OutboxLeaseDuration time.Duration
	OutboxRetryBase     time.Duration
	OutboxRetryMax      time.Duration

	PaymentPollInterval  time.Duration
	PaymentBatchSize     int
	PaymentLeaseDuration time.Duration
	PaymentRetryBase     time.Duration
	PaymentRetryMax      time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		AppName:              envOrDefault("APP_NAME", "order-system"),
		HTTPAddr:             envOrDefault("HTTP_ADDR", ":8080"),
		ShutdownTimeout:      10 * time.Second,
		ReadinessTimeout:     2 * time.Second,
		StorageDriver:        strings.ToLower(envOrDefault("STORAGE_DRIVER", StorageMemory)),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		AccessTokenTTL:       15 * time.Minute,
		RefreshTokenTTL:      30 * 24 * time.Hour,
		SecureCookies:        false,
		AuthRateWindow:       time.Minute,
		LoginRateLimit:       10,
		RefreshRateLimit:     30,
		BcryptCost:           12,
		OutboxPollInterval:   time.Second,
		OutboxBatchSize:      20,
		OutboxLeaseDuration:  30 * time.Second,
		OutboxRetryBase:      time.Second,
		OutboxRetryMax:       time.Minute,
		PaymentPollInterval:  time.Second,
		PaymentBatchSize:     10,
		PaymentLeaseDuration: 30 * time.Second,
		PaymentRetryBase:     time.Second,
		PaymentRetryMax:      time.Minute,
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
		{name: "READINESS_TIMEOUT", target: &cfg.ReadinessTimeout},
		{name: "ACCESS_TOKEN_TTL", target: &cfg.AccessTokenTTL},
		{name: "REFRESH_TOKEN_TTL", target: &cfg.RefreshTokenTTL},
		{name: "AUTH_RATE_WINDOW", target: &cfg.AuthRateWindow},
		{name: "OUTBOX_POLL_INTERVAL", target: &cfg.OutboxPollInterval},
		{name: "OUTBOX_LEASE_DURATION", target: &cfg.OutboxLeaseDuration},
		{name: "OUTBOX_RETRY_BASE", target: &cfg.OutboxRetryBase},
		{name: "OUTBOX_RETRY_MAX", target: &cfg.OutboxRetryMax},
		{name: "PAYMENT_POLL_INTERVAL", target: &cfg.PaymentPollInterval},
		{name: "PAYMENT_LEASE_DURATION", target: &cfg.PaymentLeaseDuration},
		{name: "PAYMENT_RETRY_BASE", target: &cfg.PaymentRetryBase},
		{name: "PAYMENT_RETRY_MAX", target: &cfg.PaymentRetryMax},
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
	outboxBatchSize, err := loadPositiveInt("OUTBOX_BATCH_SIZE", cfg.OutboxBatchSize)
	if err != nil {
		return Config{}, err
	}
	cfg.OutboxBatchSize = outboxBatchSize
	paymentBatchSize, err := loadPositiveInt("PAYMENT_BATCH_SIZE", cfg.PaymentBatchSize)
	if err != nil {
		return Config{}, err
	}
	cfg.PaymentBatchSize = paymentBatchSize
	if cfg.OutboxRetryMax < cfg.OutboxRetryBase {
		return Config{}, fmt.Errorf(
			"OUTBOX_RETRY_MAX must be greater than or equal to OUTBOX_RETRY_BASE",
		)
	}
	if cfg.PaymentRetryMax < cfg.PaymentRetryBase {
		return Config{}, fmt.Errorf(
			"PAYMENT_RETRY_MAX must be greater than or equal to PAYMENT_RETRY_BASE",
		)
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
