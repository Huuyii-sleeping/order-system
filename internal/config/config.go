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

	MessageDirect = "direct"
	MessageKafka  = "kafka"

	CacheNone  = "none"
	CacheRedis = "redis"
)

type Config struct {
	AppName string

	HTTPAddr          string
	OTLPTraceEndpoint string

	ShutdownTimeout  time.Duration
	ReadinessTimeout time.Duration

	StorageDriver           string
	DatabaseURL             string
	CacheDriver             string
	RedisURL                string
	RedisKeyPrefix          string
	CatalogCacheTTL         time.Duration
	CatalogNotFoundCacheTTL time.Duration
	CatalogLoadTimeout      time.Duration

	MessageDriver            string
	KafkaBrokers             string
	KafkaConsumerGroup       string
	KafkaDeadLetterTopic     string
	KafkaConsumerMaxAttempts int
	KafkaConsumerRetry       time.Duration

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

	PaymentPollInterval            time.Duration
	PaymentBatchSize               int
	PaymentWorkerConcurrency       int
	PaymentLeaseDuration           time.Duration
	PaymentRetryBase               time.Duration
	PaymentRetryMax                time.Duration
	PaymentProviderTimeout         time.Duration
	PaymentProviderMaxConcurrent   int
	PaymentCircuitFailureThreshold int
	PaymentCircuitOpenDuration     time.Duration
	SimulatedPaymentDelay          time.Duration
	SimulatedPaymentFailures       int
}

func Load() (Config, error) {
	cfg := Config{
		AppName:                        envOrDefault("APP_NAME", "order-system"),
		HTTPAddr:                       envOrDefault("HTTP_ADDR", ":8080"),
		OTLPTraceEndpoint:              strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")),
		ShutdownTimeout:                10 * time.Second,
		ReadinessTimeout:               2 * time.Second,
		StorageDriver:                  strings.ToLower(envOrDefault("STORAGE_DRIVER", StorageMemory)),
		DatabaseURL:                    os.Getenv("DATABASE_URL"),
		CacheDriver:                    strings.ToLower(envOrDefault("CACHE_DRIVER", CacheNone)),
		RedisURL:                       strings.TrimSpace(os.Getenv("REDIS_URL")),
		RedisKeyPrefix:                 envOrDefault("REDIS_KEY_PREFIX", "order-system"),
		CatalogCacheTTL:                5 * time.Minute,
		CatalogNotFoundCacheTTL:        15 * time.Second,
		CatalogLoadTimeout:             2 * time.Second,
		MessageDriver:                  strings.ToLower(envOrDefault("MESSAGE_DRIVER", MessageDirect)),
		KafkaBrokers:                   os.Getenv("KAFKA_BROKERS"),
		KafkaConsumerGroup:             envOrDefault("KAFKA_CONSUMER_GROUP", "order-system-payments-v1"),
		KafkaDeadLetterTopic:           envOrDefault("KAFKA_DEAD_LETTER_TOPIC", "orders.created.v1.dlq"),
		KafkaConsumerMaxAttempts:       3,
		KafkaConsumerRetry:             time.Second,
		AccessTokenTTL:                 15 * time.Minute,
		RefreshTokenTTL:                30 * 24 * time.Hour,
		SecureCookies:                  false,
		AuthRateWindow:                 time.Minute,
		LoginRateLimit:                 10,
		RefreshRateLimit:               30,
		BcryptCost:                     12,
		OutboxPollInterval:             time.Second,
		OutboxBatchSize:                20,
		OutboxLeaseDuration:            30 * time.Second,
		OutboxRetryBase:                time.Second,
		OutboxRetryMax:                 time.Minute,
		PaymentPollInterval:            time.Second,
		PaymentBatchSize:               10,
		PaymentWorkerConcurrency:       4,
		PaymentLeaseDuration:           30 * time.Second,
		PaymentRetryBase:               time.Second,
		PaymentRetryMax:                time.Minute,
		PaymentProviderTimeout:         2 * time.Second,
		PaymentProviderMaxConcurrent:   4,
		PaymentCircuitFailureThreshold: 5,
		PaymentCircuitOpenDuration:     30 * time.Second,
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
	switch cfg.CacheDriver {
	case CacheNone:
	case CacheRedis:
		if cfg.RedisURL == "" {
			return Config{}, fmt.Errorf("REDIS_URL is required when CACHE_DRIVER is %q", CacheRedis)
		}
		if strings.TrimSpace(cfg.RedisKeyPrefix) == "" {
			return Config{}, fmt.Errorf("REDIS_KEY_PREFIX is required when CACHE_DRIVER is %q", CacheRedis)
		}
	default:
		return Config{}, fmt.Errorf("unsupported CACHE_DRIVER %q", cfg.CacheDriver)
	}
	switch cfg.MessageDriver {
	case MessageDirect:
	case MessageKafka:
		// Kafka offset 和 Inbox 事务之间仍可能发生重复投递，因此 Kafka 模式
		// 必须使用可跨进程、跨重启保存幂等记录的 PostgreSQL。
		if cfg.StorageDriver != StoragePostgres {
			return Config{}, fmt.Errorf("MESSAGE_DRIVER %q requires STORAGE_DRIVER %q", MessageKafka, StoragePostgres)
		}
		if len(KafkaBrokerList(cfg.KafkaBrokers)) == 0 {
			return Config{}, fmt.Errorf("KAFKA_BROKERS is required when MESSAGE_DRIVER is %q", MessageKafka)
		}
		if strings.TrimSpace(cfg.KafkaConsumerGroup) == "" {
			return Config{}, fmt.Errorf("KAFKA_CONSUMER_GROUP is required when MESSAGE_DRIVER is %q", MessageKafka)
		}
		if strings.TrimSpace(cfg.KafkaDeadLetterTopic) == "" {
			return Config{}, fmt.Errorf("KAFKA_DEAD_LETTER_TOPIC is required when MESSAGE_DRIVER is %q", MessageKafka)
		}
	default:
		return Config{}, fmt.Errorf("unsupported MESSAGE_DRIVER %q", cfg.MessageDriver)
	}

	for _, setting := range []struct {
		name   string
		target *time.Duration
	}{
		{name: "SHUTDOWN_TIMEOUT", target: &cfg.ShutdownTimeout},
		{name: "READINESS_TIMEOUT", target: &cfg.ReadinessTimeout},
		{name: "CATALOG_CACHE_TTL", target: &cfg.CatalogCacheTTL},
		{name: "CATALOG_NOT_FOUND_CACHE_TTL", target: &cfg.CatalogNotFoundCacheTTL},
		{name: "CATALOG_LOAD_TIMEOUT", target: &cfg.CatalogLoadTimeout},
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
		{name: "PAYMENT_PROVIDER_TIMEOUT", target: &cfg.PaymentProviderTimeout},
		{name: "PAYMENT_CIRCUIT_OPEN_DURATION", target: &cfg.PaymentCircuitOpenDuration},
		{name: "KAFKA_CONSUMER_RETRY", target: &cfg.KafkaConsumerRetry},
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
	paymentWorkerConcurrency, err := loadPositiveInt(
		"PAYMENT_WORKER_CONCURRENCY", cfg.PaymentWorkerConcurrency,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.PaymentWorkerConcurrency = paymentWorkerConcurrency
	paymentProviderMaxConcurrent, err := loadPositiveInt(
		"PAYMENT_PROVIDER_MAX_CONCURRENT", cfg.PaymentProviderMaxConcurrent,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.PaymentProviderMaxConcurrent = paymentProviderMaxConcurrent
	paymentCircuitFailureThreshold, err := loadPositiveInt(
		"PAYMENT_CIRCUIT_FAILURE_THRESHOLD", cfg.PaymentCircuitFailureThreshold,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.PaymentCircuitFailureThreshold = paymentCircuitFailureThreshold
	simulatedPaymentDelay, err := loadNonNegativeDuration(
		"SIMULATED_PAYMENT_DELAY", cfg.SimulatedPaymentDelay,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.SimulatedPaymentDelay = simulatedPaymentDelay
	simulatedPaymentFailures, err := loadNonNegativeInt(
		"SIMULATED_PAYMENT_FAILURES", cfg.SimulatedPaymentFailures,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.SimulatedPaymentFailures = simulatedPaymentFailures
	kafkaConsumerMaxAttempts, err := loadPositiveInt("KAFKA_CONSUMER_MAX_ATTEMPTS", cfg.KafkaConsumerMaxAttempts)
	if err != nil {
		return Config{}, err
	}
	cfg.KafkaConsumerMaxAttempts = kafkaConsumerMaxAttempts
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
	providerCallRounds := cfg.PaymentBatchSize / cfg.PaymentWorkerConcurrency
	if cfg.PaymentBatchSize%cfg.PaymentWorkerConcurrency != 0 {
		providerCallRounds++
	}
	// 使用除法避免极端配置下 duration 乘法溢出。向下取整会略微保守，
	// 但能保证最坏调用时间不会吃完整个任务租约。
	if cfg.PaymentProviderTimeout >= cfg.PaymentLeaseDuration/time.Duration(providerCallRounds) {
		return Config{}, fmt.Errorf(
			"payment batch timeout budget must be less than PAYMENT_LEASE_DURATION",
		)
	}

	return cfg, nil
}

func KafkaBrokerList(value string) []string {
	parts := strings.Split(value, ",")
	brokers := make([]string, 0, len(parts))
	for _, part := range parts {
		if broker := strings.TrimSpace(part); broker != "" {
			brokers = append(brokers, broker)
		}
	}
	return brokers
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

func loadNonNegativeDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, raw, err)
	}
	if duration < 0 {
		return 0, fmt.Errorf("%s %q must not be negative", name, raw)
	}
	return duration, nil
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

func loadNonNegativeInt(name string, fallback int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, raw, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s %q must not be negative", name, raw)
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
