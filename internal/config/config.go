package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	LogDebug = "debug"
	LogInfo  = "info"
	LogWarn  = "warn"
	LogError = "error"

	StorageMemory   = "memory"
	StoragePostgres = "postgres"

	MessageDirect = "direct"
	MessageKafka  = "kafka"

	CacheNone  = "none"
	CacheRedis = "redis"
)

type Config struct {
	AppName  string
	LogLevel string

	HTTPAddr                        string
	OTLPTraceEndpoint               string
	DiagnosticsEnabled              bool
	DiagnosticsAddr                 string
	DiagnosticsMutexProfileFraction int
	DiagnosticsBlockProfileRate     int

	ShutdownTimeout    time.Duration
	ShutdownDrainDelay time.Duration
	ReadinessTimeout   time.Duration

	StorageDriver           string
	DatabaseURL             string
	DatabaseMaxOpenConns    int
	DatabaseMaxIdleConns    int
	DatabaseConnMaxLifetime time.Duration
	DatabaseConnMaxIdleTime time.Duration
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

	AccessTokenTTL             time.Duration
	RefreshTokenTTL            time.Duration
	CredentialCleanupInterval  time.Duration
	CredentialCleanupBatchSize int
	SecureCookies              bool
	AuthRateWindow             time.Duration
	LoginRateLimit             int
	RefreshRateLimit           int
	BcryptCost                 int

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

	NotificationPollInterval            time.Duration
	NotificationBatchSize               int
	NotificationWorkerConcurrency       int
	NotificationLeaseDuration           time.Duration
	NotificationRetryBase               time.Duration
	NotificationRetryMax                time.Duration
	NotificationProviderTimeout         time.Duration
	NotificationWebhookSecret           string
	NotificationWebhookTolerance        time.Duration
	SimulatedNotificationDelay          time.Duration
	SimulatedNotificationFailures       int
	SimulatedNotificationRejections     int
	SimulatedNotificationResponseLosses int
}

func Load() (Config, error) {
	cfg := Config{
		AppName:                        envOrDefault("APP_NAME", "order-system"),
		LogLevel:                       strings.ToLower(strings.TrimSpace(envOrDefault("LOG_LEVEL", LogInfo))),
		HTTPAddr:                       envOrDefault("HTTP_ADDR", ":8080"),
		OTLPTraceEndpoint:              strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")),
		DiagnosticsAddr:                strings.TrimSpace(envOrDefault("DIAGNOSTICS_ADDR", "127.0.0.1:6060")),
		ShutdownTimeout:                10 * time.Second,
		ShutdownDrainDelay:             2 * time.Second,
		ReadinessTimeout:               2 * time.Second,
		StorageDriver:                  strings.ToLower(envOrDefault("STORAGE_DRIVER", StorageMemory)),
		DatabaseURL:                    os.Getenv("DATABASE_URL"),
		DatabaseMaxOpenConns:           20,
		DatabaseMaxIdleConns:           5,
		DatabaseConnMaxLifetime:        30 * time.Minute,
		DatabaseConnMaxIdleTime:        5 * time.Minute,
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
		CredentialCleanupInterval:      time.Hour,
		CredentialCleanupBatchSize:     500,
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
		NotificationPollInterval:       time.Second,
		NotificationBatchSize:          10,
		NotificationWorkerConcurrency:  4,
		NotificationLeaseDuration:      30 * time.Second,
		NotificationRetryBase:          time.Second,
		NotificationRetryMax:           time.Minute,
		NotificationProviderTimeout:    2 * time.Second,
		NotificationWebhookSecret:      strings.TrimSpace(os.Getenv("NOTIFICATION_WEBHOOK_SECRET")),
		NotificationWebhookTolerance:   5 * time.Minute,
	}

	switch cfg.LogLevel {
	case LogDebug, LogInfo, LogWarn, LogError:
	default:
		return Config{}, fmt.Errorf("unsupported LOG_LEVEL %q", cfg.LogLevel)
	}
	diagnosticsEnabled, err := loadBool("DIAGNOSTICS_ENABLED", cfg.DiagnosticsEnabled)
	if err != nil {
		return Config{}, err
	}
	cfg.DiagnosticsEnabled = diagnosticsEnabled
	diagnosticsMutexProfileFraction, err := loadNonNegativeInt(
		"DIAGNOSTICS_MUTEX_PROFILE_FRACTION", cfg.DiagnosticsMutexProfileFraction,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.DiagnosticsMutexProfileFraction = diagnosticsMutexProfileFraction
	diagnosticsBlockProfileRate, err := loadNonNegativeInt(
		"DIAGNOSTICS_BLOCK_PROFILE_RATE", cfg.DiagnosticsBlockProfileRate,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.DiagnosticsBlockProfileRate = diagnosticsBlockProfileRate
	if cfg.DiagnosticsEnabled {
		if err := validateDiagnosticsAddress(cfg.DiagnosticsAddr); err != nil {
			return Config{}, err
		}
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
		{name: "SHUTDOWN_DRAIN_DELAY", target: &cfg.ShutdownDrainDelay},
		{name: "READINESS_TIMEOUT", target: &cfg.ReadinessTimeout},
		{name: "DATABASE_CONN_MAX_LIFETIME", target: &cfg.DatabaseConnMaxLifetime},
		{name: "DATABASE_CONN_MAX_IDLE_TIME", target: &cfg.DatabaseConnMaxIdleTime},
		{name: "CATALOG_CACHE_TTL", target: &cfg.CatalogCacheTTL},
		{name: "CATALOG_NOT_FOUND_CACHE_TTL", target: &cfg.CatalogNotFoundCacheTTL},
		{name: "CATALOG_LOAD_TIMEOUT", target: &cfg.CatalogLoadTimeout},
		{name: "ACCESS_TOKEN_TTL", target: &cfg.AccessTokenTTL},
		{name: "REFRESH_TOKEN_TTL", target: &cfg.RefreshTokenTTL},
		{name: "AUTH_CREDENTIAL_CLEANUP_INTERVAL", target: &cfg.CredentialCleanupInterval},
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
		{name: "NOTIFICATION_POLL_INTERVAL", target: &cfg.NotificationPollInterval},
		{name: "NOTIFICATION_LEASE_DURATION", target: &cfg.NotificationLeaseDuration},
		{name: "NOTIFICATION_RETRY_BASE", target: &cfg.NotificationRetryBase},
		{name: "NOTIFICATION_RETRY_MAX", target: &cfg.NotificationRetryMax},
		{name: "NOTIFICATION_PROVIDER_TIMEOUT", target: &cfg.NotificationProviderTimeout},
		{name: "NOTIFICATION_WEBHOOK_TOLERANCE", target: &cfg.NotificationWebhookTolerance},
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
	databaseMaxOpenConns, err := loadPositiveInt("DATABASE_MAX_OPEN_CONNS", cfg.DatabaseMaxOpenConns)
	if err != nil {
		return Config{}, err
	}
	cfg.DatabaseMaxOpenConns = databaseMaxOpenConns
	databaseMaxIdleConns, err := loadPositiveInt("DATABASE_MAX_IDLE_CONNS", cfg.DatabaseMaxIdleConns)
	if err != nil {
		return Config{}, err
	}
	cfg.DatabaseMaxIdleConns = databaseMaxIdleConns
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
	credentialCleanupBatchSize, err := loadPositiveInt(
		"AUTH_CREDENTIAL_CLEANUP_BATCH_SIZE", cfg.CredentialCleanupBatchSize,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.CredentialCleanupBatchSize = credentialCleanupBatchSize
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
	simulatedNotificationDelay, err := loadNonNegativeDuration(
		"SIMULATED_NOTIFICATION_DELAY", cfg.SimulatedNotificationDelay,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.SimulatedNotificationDelay = simulatedNotificationDelay
	simulatedNotificationFailures, err := loadNonNegativeInt(
		"SIMULATED_NOTIFICATION_FAILURES", cfg.SimulatedNotificationFailures,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.SimulatedNotificationFailures = simulatedNotificationFailures
	simulatedNotificationRejections, err := loadNonNegativeInt(
		"SIMULATED_NOTIFICATION_REJECTIONS", cfg.SimulatedNotificationRejections,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.SimulatedNotificationRejections = simulatedNotificationRejections
	simulatedNotificationResponseLosses, err := loadNonNegativeInt(
		"SIMULATED_NOTIFICATION_RESPONSE_LOSSES", cfg.SimulatedNotificationResponseLosses,
	)
	if err != nil {
		return Config{}, err
	}
	cfg.SimulatedNotificationResponseLosses = simulatedNotificationResponseLosses
	notificationBatchSize, err := loadPositiveInt("NOTIFICATION_BATCH_SIZE", cfg.NotificationBatchSize)
	if err != nil {
		return Config{}, err
	}
	cfg.NotificationBatchSize = notificationBatchSize
	notificationWorkerConcurrency, err := loadPositiveInt("NOTIFICATION_WORKER_CONCURRENCY", cfg.NotificationWorkerConcurrency)
	if err != nil {
		return Config{}, err
	}
	cfg.NotificationWorkerConcurrency = notificationWorkerConcurrency
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
	if cfg.DatabaseMaxIdleConns > cfg.DatabaseMaxOpenConns {
		return Config{}, fmt.Errorf("DATABASE_MAX_IDLE_CONNS must not exceed DATABASE_MAX_OPEN_CONNS")
	}
	if cfg.PaymentRetryMax < cfg.PaymentRetryBase {
		return Config{}, fmt.Errorf(
			"PAYMENT_RETRY_MAX must be greater than or equal to PAYMENT_RETRY_BASE",
		)
	}
	if cfg.NotificationRetryMax < cfg.NotificationRetryBase {
		return Config{}, fmt.Errorf("NOTIFICATION_RETRY_MAX must be greater than or equal to NOTIFICATION_RETRY_BASE")
	}
	if cfg.NotificationWebhookSecret != "" && len(cfg.NotificationWebhookSecret) < 32 {
		return Config{}, fmt.Errorf("NOTIFICATION_WEBHOOK_SECRET must be at least 32 bytes when configured")
	}
	if cfg.ShutdownDrainDelay >= cfg.ShutdownTimeout {
		return Config{}, fmt.Errorf(
			"SHUTDOWN_DRAIN_DELAY must be less than SHUTDOWN_TIMEOUT",
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
	notificationCallRounds := cfg.NotificationBatchSize / cfg.NotificationWorkerConcurrency
	if cfg.NotificationBatchSize%cfg.NotificationWorkerConcurrency != 0 {
		notificationCallRounds++
	}
	// unknown/过期 processing 任务最坏会先 Lookup 再 Send，因此每一轮需要预留
	// 两次 Provider 调用预算。连续除法避免极端配置下整数乘法溢出。
	if cfg.NotificationProviderTimeout >= cfg.NotificationLeaseDuration/time.Duration(notificationCallRounds)/2 {
		return Config{}, fmt.Errorf("notification batch timeout budget must be less than NOTIFICATION_LEASE_DURATION")
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

func validateDiagnosticsAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse DIAGNOSTICS_ADDR %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("DIAGNOSTICS_ADDR %q must use an explicit loopback IP", address)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("DIAGNOSTICS_ADDR %q must use a port between 1 and 65535", address)
	}
	return nil
}

func envOrDefault(name string, fallback string) string {
	value := os.Getenv(name)

	if value != "" {
		return value
	}
	return fallback
}
