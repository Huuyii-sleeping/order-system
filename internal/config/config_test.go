// config_test 使用外部测试包，模拟真正的调用方使用配置包。
//
// 这样测试只能访问 config 包公开的类型和函数，
// 可以避免测试依赖内部实现细节。
package config_test

import (
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name string

		// 每个测试场景会把这些值写入环境变量。
		// 空字符串表示模拟“没有配置该环境变量”。
		appName                             string
		httpAddr                            string
		otlpTraceEndpoint                   string
		shutdownTimeout                     string
		shutdownDrainDelay                  string
		readinessTimeout                    string
		storageDriver                       string
		databaseURL                         string
		cacheDriver                         string
		redisURL                            string
		redisKeyPrefix                      string
		catalogCacheTTL                     string
		catalogNotFoundCacheTTL             string
		catalogLoadTimeout                  string
		accessTokenTTL                      string
		refreshTokenTTL                     string
		credentialCleanupInterval           string
		credentialCleanupBatchSize          string
		secureCookies                       string
		authRateWindow                      string
		loginRateLimit                      string
		refreshRateLimit                    string
		outboxPollInterval                  string
		outboxBatchSize                     string
		outboxLeaseDuration                 string
		outboxRetryBase                     string
		outboxRetryMax                      string
		paymentPollInterval                 string
		paymentBatchSize                    string
		paymentWorkerConcurrency            string
		paymentLeaseDuration                string
		paymentRetryBase                    string
		paymentRetryMax                     string
		paymentProviderTimeout              string
		paymentProviderMaxConcurrent        string
		paymentCircuitFailureThreshold      string
		paymentCircuitOpenDuration          string
		simulatedPaymentDelay               string
		simulatedPaymentFailures            string
		messageDriver                       string
		kafkaBrokers                        string
		kafkaConsumerGroup                  string
		kafkaDeadLetterTopic                string
		kafkaConsumerMaxAttempts            string
		kafkaConsumerRetry                  string
		notificationPollInterval            string
		notificationBatchSize               string
		notificationWorkerConcurrency       string
		notificationLeaseDuration           string
		notificationRetryBase               string
		notificationRetryMax                string
		notificationProviderTimeout         string
		notificationWebhookSecret           string
		notificationWebhookTolerance        string
		simulatedNotificationDelay          string
		simulatedNotificationFailures       string
		simulatedNotificationRejections     string
		simulatedNotificationResponseLosses string

		want              config.Config
		wantErrorContains string
	}{
		{
			name:            "uses defaults when environment variables are empty",
			appName:         "",
			httpAddr:        "",
			shutdownTimeout: "",
			want: config.Config{
				AppName:                    "order-system",
				HTTPAddr:                   ":8080",
				ShutdownTimeout:            10 * time.Second,
				ShutdownDrainDelay:         2 * time.Second,
				ReadinessTimeout:           2 * time.Second,
				StorageDriver:              config.StorageMemory,
				AccessTokenTTL:             15 * time.Minute,
				RefreshTokenTTL:            30 * 24 * time.Hour,
				CredentialCleanupInterval:  time.Hour,
				CredentialCleanupBatchSize: 500,
				AuthRateWindow:             time.Minute,
				LoginRateLimit:             10,
				RefreshRateLimit:           30,
				BcryptCost:                 12,
				OutboxPollInterval:         time.Second,
				OutboxBatchSize:            20,
				OutboxLeaseDuration:        30 * time.Second,
				OutboxRetryBase:            time.Second,
				OutboxRetryMax:             time.Minute,
				PaymentPollInterval:        time.Second,
				PaymentBatchSize:           10,
				PaymentLeaseDuration:       30 * time.Second,
				PaymentRetryBase:           time.Second,
				PaymentRetryMax:            time.Minute,
			},
		},
		{
			name:            "environment variables override token lifetimes",
			accessTokenTTL:  "5m",
			refreshTokenTTL: "12h",
			want: config.Config{
				AppName:              "order-system",
				HTTPAddr:             ":8080",
				ShutdownTimeout:      10 * time.Second,
				ShutdownDrainDelay:   2 * time.Second,
				ReadinessTimeout:     2 * time.Second,
				StorageDriver:        config.StorageMemory,
				AccessTokenTTL:       5 * time.Minute,
				RefreshTokenTTL:      12 * time.Hour,
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
			},
		},
		{
			name:                       "environment variables override credential cleanup",
			credentialCleanupInterval:  "15m",
			credentialCleanupBatchSize: "75",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				ShutdownTimeout: 10 * time.Second, ShutdownDrainDelay: 2 * time.Second,
				ReadinessTimeout: 2 * time.Second, StorageDriver: config.StorageMemory,
				AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 30 * 24 * time.Hour,
				CredentialCleanupInterval: 15 * time.Minute, CredentialCleanupBatchSize: 75,
				AuthRateWindow: time.Minute, LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second, OutboxRetryMax: time.Minute,
				PaymentPollInterval: time.Second, PaymentBatchSize: 10, PaymentLeaseDuration: 30 * time.Second,
				PaymentRetryBase: time.Second, PaymentRetryMax: time.Minute,
			},
		},
		{
			name:            "environment variables override defaults",
			appName:         "checkout-service",
			httpAddr:        ":9090",
			shutdownTimeout: "30s",
			storageDriver:   config.StoragePostgres,
			databaseURL:     "postgres://localhost/order_system",
			want: config.Config{
				AppName:              "checkout-service",
				HTTPAddr:             ":9090",
				ShutdownTimeout:      30 * time.Second,
				ShutdownDrainDelay:   2 * time.Second,
				ReadinessTimeout:     2 * time.Second,
				StorageDriver:        config.StoragePostgres,
				DatabaseURL:          "postgres://localhost/order_system",
				AccessTokenTTL:       15 * time.Minute,
				RefreshTokenTTL:      30 * 24 * time.Hour,
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
			},
		},
		{
			name:               "environment variables override shutdown drain delay",
			shutdownTimeout:    "30s",
			shutdownDrainDelay: "5s",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				ShutdownTimeout: 30 * time.Second, ShutdownDrainDelay: 5 * time.Second,
				ReadinessTimeout: 2 * time.Second, StorageDriver: config.StorageMemory,
				AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 30 * 24 * time.Hour,
				AuthRateWindow: time.Minute, LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second, OutboxRetryMax: time.Minute,
				PaymentPollInterval: time.Second, PaymentBatchSize: 10, PaymentLeaseDuration: 30 * time.Second,
				PaymentRetryBase: time.Second, PaymentRetryMax: time.Minute,
			},
		},
		{
			name:             "environment variables override auth security",
			secureCookies:    "true",
			authRateWindow:   "30s",
			loginRateLimit:   "4",
			refreshRateLimit: "12",
			want: config.Config{
				AppName:              "order-system",
				HTTPAddr:             ":8080",
				ShutdownTimeout:      10 * time.Second,
				ReadinessTimeout:     2 * time.Second,
				StorageDriver:        config.StorageMemory,
				AccessTokenTTL:       15 * time.Minute,
				RefreshTokenTTL:      30 * 24 * time.Hour,
				SecureCookies:        true,
				AuthRateWindow:       30 * time.Second,
				LoginRateLimit:       4,
				RefreshRateLimit:     12,
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
			},
		},
		{
			name:                "environment variables override outbox worker",
			outboxPollInterval:  "250ms",
			outboxBatchSize:     "5",
			outboxLeaseDuration: "10s",
			outboxRetryBase:     "2s",
			outboxRetryMax:      "30s",
			want: config.Config{
				AppName:              "order-system",
				HTTPAddr:             ":8080",
				ShutdownTimeout:      10 * time.Second,
				ReadinessTimeout:     2 * time.Second,
				StorageDriver:        config.StorageMemory,
				AccessTokenTTL:       15 * time.Minute,
				RefreshTokenTTL:      30 * 24 * time.Hour,
				AuthRateWindow:       time.Minute,
				LoginRateLimit:       10,
				RefreshRateLimit:     30,
				BcryptCost:           12,
				OutboxPollInterval:   250 * time.Millisecond,
				OutboxBatchSize:      5,
				OutboxLeaseDuration:  10 * time.Second,
				OutboxRetryBase:      2 * time.Second,
				OutboxRetryMax:       30 * time.Second,
				PaymentPollInterval:  time.Second,
				PaymentBatchSize:     10,
				PaymentLeaseDuration: 30 * time.Second,
				PaymentRetryBase:     time.Second,
				PaymentRetryMax:      time.Minute,
			},
		},
		{
			name:                           "environment variables override payment worker",
			paymentPollInterval:            "300ms",
			paymentBatchSize:               "4",
			paymentWorkerConcurrency:       "2",
			paymentLeaseDuration:           "15s",
			paymentRetryBase:               "3s",
			paymentRetryMax:                "45s",
			paymentProviderTimeout:         "750ms",
			paymentProviderMaxConcurrent:   "3",
			paymentCircuitFailureThreshold: "7",
			paymentCircuitOpenDuration:     "20s",
			simulatedPaymentDelay:          "10ms",
			simulatedPaymentFailures:       "2",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				ShutdownTimeout: 10 * time.Second, ReadinessTimeout: 2 * time.Second,
				StorageDriver: config.StorageMemory, AccessTokenTTL: 15 * time.Minute,
				RefreshTokenTTL: 30 * 24 * time.Hour, AuthRateWindow: time.Minute,
				LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second,
				OutboxRetryMax: time.Minute, PaymentPollInterval: 300 * time.Millisecond,
				PaymentBatchSize: 4, PaymentWorkerConcurrency: 2,
				PaymentLeaseDuration: 15 * time.Second, PaymentRetryBase: 3 * time.Second,
				PaymentRetryMax: 45 * time.Second, PaymentProviderTimeout: 750 * time.Millisecond,
				PaymentProviderMaxConcurrent: 3, PaymentCircuitFailureThreshold: 7,
				PaymentCircuitOpenDuration: 20 * time.Second,
				SimulatedPaymentDelay:      10 * time.Millisecond, SimulatedPaymentFailures: 2,
			},
		},
		{
			name:                     "configures kafka messaging with postgres",
			storageDriver:            config.StoragePostgres,
			databaseURL:              "postgres://localhost/order_system",
			messageDriver:            config.MessageKafka,
			kafkaBrokers:             " broker-a:9092,broker-b:9092 ",
			kafkaConsumerGroup:       "payments-test",
			kafkaDeadLetterTopic:     "orders.created.v1.failed",
			kafkaConsumerMaxAttempts: "5",
			kafkaConsumerRetry:       "250ms",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				ShutdownTimeout: 10 * time.Second, ReadinessTimeout: 2 * time.Second,
				StorageDriver: config.StoragePostgres, DatabaseURL: "postgres://localhost/order_system",
				MessageDriver: config.MessageKafka, KafkaBrokers: " broker-a:9092,broker-b:9092 ",
				KafkaConsumerGroup: "payments-test", KafkaDeadLetterTopic: "orders.created.v1.failed",
				KafkaConsumerMaxAttempts: 5, KafkaConsumerRetry: 250 * time.Millisecond,
				AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 30 * 24 * time.Hour,
				AuthRateWindow: time.Minute, LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second, OutboxRetryMax: time.Minute,
				PaymentPollInterval: time.Second, PaymentBatchSize: 10,
				PaymentLeaseDuration: 30 * time.Second, PaymentRetryBase: time.Second, PaymentRetryMax: time.Minute,
			},
		},
		{
			name:              "configures OTLP trace export",
			otlpTraceEndpoint: "http://127.0.0.1:4318/v1/traces",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				OTLPTraceEndpoint: "http://127.0.0.1:4318/v1/traces",
				ShutdownTimeout:   10 * time.Second, ReadinessTimeout: 2 * time.Second,
				StorageDriver: config.StorageMemory, AccessTokenTTL: 15 * time.Minute,
				RefreshTokenTTL: 30 * 24 * time.Hour, AuthRateWindow: time.Minute,
				LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second,
				OutboxRetryMax: time.Minute, PaymentPollInterval: time.Second,
				PaymentBatchSize: 10, PaymentLeaseDuration: 30 * time.Second,
				PaymentRetryBase: time.Second, PaymentRetryMax: time.Minute,
			},
		},
		{
			name:                    "configures redis cache and shared coordination",
			cacheDriver:             config.CacheRedis,
			redisURL:                "redis://127.0.0.1:6379/0",
			redisKeyPrefix:          "learning",
			catalogCacheTTL:         "2m",
			catalogNotFoundCacheTTL: "5s",
			catalogLoadTimeout:      "750ms",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				ShutdownTimeout: 10 * time.Second, ReadinessTimeout: 2 * time.Second,
				StorageDriver: config.StorageMemory, CacheDriver: config.CacheRedis,
				RedisURL: "redis://127.0.0.1:6379/0", RedisKeyPrefix: "learning",
				CatalogCacheTTL: 2 * time.Minute, CatalogNotFoundCacheTTL: 5 * time.Second,
				CatalogLoadTimeout: 750 * time.Millisecond,
				AccessTokenTTL:     15 * time.Minute, RefreshTokenTTL: 30 * 24 * time.Hour,
				AuthRateWindow: time.Minute, LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second, OutboxRetryMax: time.Minute,
				PaymentPollInterval: time.Second, PaymentBatchSize: 10,
				PaymentLeaseDuration: 30 * time.Second, PaymentRetryBase: time.Second, PaymentRetryMax: time.Minute,
			},
		},
		{
			name: "redis cache requires URL", cacheDriver: config.CacheRedis,
			wantErrorContains: "REDIS_URL",
		},
		{
			name: "rejects unsupported cache driver", cacheDriver: "memcached",
			wantErrorContains: "unsupported CACHE_DRIVER",
		},
		{
			name:              "rejects an invalid shutdown timeout",
			shutdownTimeout:   "ten-seconds",
			wantErrorContains: "parse SHUTDOWN_TIMEOUT",
		},
		{
			name:              "rejects a zero shutdown timeout",
			shutdownTimeout:   "0s",
			wantErrorContains: "must be greater than zero",
		},
		{
			name:              "rejects a negative shutdown timeout",
			shutdownTimeout:   "-5s",
			wantErrorContains: "must be greater than zero",
		},
		{
			name:              "rejects zero readiness timeout",
			readinessTimeout:  "0s",
			wantErrorContains: "READINESS_TIMEOUT",
		},
		{
			name:                      "rejects zero credential cleanup interval",
			credentialCleanupInterval: "0s",
			wantErrorContains:         "AUTH_CREDENTIAL_CLEANUP_INTERVAL",
		},
		{
			name:                       "rejects zero credential cleanup batch size",
			credentialCleanupBatchSize: "0",
			wantErrorContains:          "AUTH_CREDENTIAL_CLEANUP_BATCH_SIZE",
		},
		{
			name: "rejects drain delay equal to shutdown timeout", shutdownDrainDelay: "10s",
			wantErrorContains: "SHUTDOWN_DRAIN_DELAY must be less than SHUTDOWN_TIMEOUT",
		},
		{
			name:              "rejects unsupported storage driver",
			storageDriver:     "mongodb",
			wantErrorContains: "unsupported STORAGE_DRIVER",
		},
		{
			name:              "postgres requires database URL",
			storageDriver:     config.StoragePostgres,
			wantErrorContains: "DATABASE_URL is required",
		},
		{
			name:              "rejects invalid secure cookie flag",
			secureCookies:     "sometimes",
			wantErrorContains: "parse SECURE_COOKIES",
		},
		{
			name:              "rejects zero login rate limit",
			loginRateLimit:    "0",
			wantErrorContains: "LOGIN_RATE_LIMIT",
		},
		{
			name:              "rejects invalid refresh rate limit",
			refreshRateLimit:  "many",
			wantErrorContains: "parse REFRESH_RATE_LIMIT",
		},
		{
			name:              "rejects zero outbox batch size",
			outboxBatchSize:   "0",
			wantErrorContains: "OUTBOX_BATCH_SIZE",
		},
		{
			name:              "rejects outbox retry max below base",
			outboxRetryBase:   "10s",
			outboxRetryMax:    "5s",
			wantErrorContains: "OUTBOX_RETRY_MAX",
		},
		{
			name:              "rejects zero payment batch size",
			paymentBatchSize:  "0",
			wantErrorContains: "PAYMENT_BATCH_SIZE",
		},
		{
			name:                     "rejects zero payment worker concurrency",
			paymentWorkerConcurrency: "0",
			wantErrorContains:        "PAYMENT_WORKER_CONCURRENCY",
		},
		{
			name:              "rejects payment retry max below base",
			paymentRetryBase:  "10s",
			paymentRetryMax:   "5s",
			wantErrorContains: "PAYMENT_RETRY_MAX",
		},
		{
			name:                   "rejects provider timeout that can outlive task lease",
			paymentProviderTimeout: "30s",
			wantErrorContains:      "payment batch timeout budget",
		},
		{
			name:                     "rejects negative simulated provider failures",
			simulatedPaymentFailures: "-1",
			wantErrorContains:        "SIMULATED_PAYMENT_FAILURES",
		},
		{
			name:                                "configures simulated lost notification responses",
			simulatedNotificationResponseLosses: "2",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				ShutdownTimeout: 10 * time.Second, ShutdownDrainDelay: 2 * time.Second,
				ReadinessTimeout: 2 * time.Second, StorageDriver: config.StorageMemory,
				AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 30 * 24 * time.Hour,
				AuthRateWindow: time.Minute, LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second, OutboxRetryMax: time.Minute,
				PaymentPollInterval: time.Second, PaymentBatchSize: 10,
				PaymentLeaseDuration: 30 * time.Second, PaymentRetryBase: time.Second, PaymentRetryMax: time.Minute,
				SimulatedNotificationResponseLosses: 2,
			},
		},
		{
			name:                         "configures notification webhook security",
			notificationWebhookSecret:    "local-test-secret-with-32-bytes-min",
			notificationWebhookTolerance: "2m",
			want: config.Config{
				AppName: "order-system", HTTPAddr: ":8080",
				ShutdownTimeout: 10 * time.Second, ShutdownDrainDelay: 2 * time.Second,
				ReadinessTimeout: 2 * time.Second, StorageDriver: config.StorageMemory,
				AccessTokenTTL: 15 * time.Minute, RefreshTokenTTL: 30 * 24 * time.Hour,
				AuthRateWindow: time.Minute, LoginRateLimit: 10, RefreshRateLimit: 30, BcryptCost: 12,
				OutboxPollInterval: time.Second, OutboxBatchSize: 20,
				OutboxLeaseDuration: 30 * time.Second, OutboxRetryBase: time.Second, OutboxRetryMax: time.Minute,
				PaymentPollInterval: time.Second, PaymentBatchSize: 10,
				PaymentLeaseDuration: 30 * time.Second, PaymentRetryBase: time.Second, PaymentRetryMax: time.Minute,
				NotificationWebhookSecret: "local-test-secret-with-32-bytes-min", NotificationWebhookTolerance: 2 * time.Minute,
			},
		},
		{
			name: "rejects short notification webhook secret", notificationWebhookSecret: "too-short",
			wantErrorContains: "NOTIFICATION_WEBHOOK_SECRET",
		},
		{
			name: "rejects zero notification webhook tolerance", notificationWebhookTolerance: "0s",
			wantErrorContains: "NOTIFICATION_WEBHOOK_TOLERANCE",
		},
		{
			name:                                "rejects negative simulated notification response losses",
			simulatedNotificationResponseLosses: "-1",
			wantErrorContains:                   "SIMULATED_NOTIFICATION_RESPONSE_LOSSES",
		},
		{
			name:                         "rejects zero provider concurrency",
			paymentProviderMaxConcurrent: "0",
			wantErrorContains:            "PAYMENT_PROVIDER_MAX_CONCURRENT",
		},
		{
			name:              "rejects unsupported message driver",
			messageDriver:     "rabbitmq",
			wantErrorContains: "unsupported MESSAGE_DRIVER",
		},
		{
			name:              "kafka requires postgres storage",
			messageDriver:     config.MessageKafka,
			kafkaBrokers:      "127.0.0.1:9092",
			wantErrorContains: "requires STORAGE_DRIVER",
		},
		{
			name:              "kafka requires brokers",
			storageDriver:     config.StoragePostgres,
			databaseURL:       "postgres://localhost/order_system",
			messageDriver:     config.MessageKafka,
			wantErrorContains: "KAFKA_BROKERS",
		},
		{
			name:                 "kafka requires dead letter topic",
			storageDriver:        config.StoragePostgres,
			databaseURL:          "postgres://localhost/order_system",
			messageDriver:        config.MessageKafka,
			kafkaBrokers:         "127.0.0.1:9092",
			kafkaDeadLetterTopic: " ",
			wantErrorContains:    "KAFKA_DEAD_LETTER_TOPIC",
		},
		{
			name:                     "rejects zero kafka consumer attempts",
			kafkaConsumerMaxAttempts: "0",
			wantErrorContains:        "KAFKA_CONSUMER_MAX_ATTEMPTS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv 会在子测试结束后自动恢复原来的环境变量。
			// 因此每个用例都不会污染其他用例。
			t.Setenv("APP_NAME", tt.appName)
			t.Setenv("HTTP_ADDR", tt.httpAddr)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", tt.otlpTraceEndpoint)
			t.Setenv("SHUTDOWN_TIMEOUT", tt.shutdownTimeout)
			t.Setenv("SHUTDOWN_DRAIN_DELAY", tt.shutdownDrainDelay)
			t.Setenv("READINESS_TIMEOUT", tt.readinessTimeout)
			t.Setenv("STORAGE_DRIVER", tt.storageDriver)
			t.Setenv("DATABASE_URL", tt.databaseURL)
			t.Setenv("CACHE_DRIVER", tt.cacheDriver)
			t.Setenv("REDIS_URL", tt.redisURL)
			t.Setenv("REDIS_KEY_PREFIX", tt.redisKeyPrefix)
			t.Setenv("CATALOG_CACHE_TTL", tt.catalogCacheTTL)
			t.Setenv("CATALOG_NOT_FOUND_CACHE_TTL", tt.catalogNotFoundCacheTTL)
			t.Setenv("CATALOG_LOAD_TIMEOUT", tt.catalogLoadTimeout)
			t.Setenv("ACCESS_TOKEN_TTL", tt.accessTokenTTL)
			t.Setenv("REFRESH_TOKEN_TTL", tt.refreshTokenTTL)
			t.Setenv("AUTH_CREDENTIAL_CLEANUP_INTERVAL", tt.credentialCleanupInterval)
			t.Setenv("AUTH_CREDENTIAL_CLEANUP_BATCH_SIZE", tt.credentialCleanupBatchSize)
			t.Setenv("SECURE_COOKIES", tt.secureCookies)
			t.Setenv("AUTH_RATE_WINDOW", tt.authRateWindow)
			t.Setenv("LOGIN_RATE_LIMIT", tt.loginRateLimit)
			t.Setenv("REFRESH_RATE_LIMIT", tt.refreshRateLimit)
			t.Setenv("OUTBOX_POLL_INTERVAL", tt.outboxPollInterval)
			t.Setenv("OUTBOX_BATCH_SIZE", tt.outboxBatchSize)
			t.Setenv("OUTBOX_LEASE_DURATION", tt.outboxLeaseDuration)
			t.Setenv("OUTBOX_RETRY_BASE", tt.outboxRetryBase)
			t.Setenv("OUTBOX_RETRY_MAX", tt.outboxRetryMax)
			t.Setenv("PAYMENT_POLL_INTERVAL", tt.paymentPollInterval)
			t.Setenv("PAYMENT_BATCH_SIZE", tt.paymentBatchSize)
			t.Setenv("PAYMENT_WORKER_CONCURRENCY", tt.paymentWorkerConcurrency)
			t.Setenv("PAYMENT_LEASE_DURATION", tt.paymentLeaseDuration)
			t.Setenv("PAYMENT_RETRY_BASE", tt.paymentRetryBase)
			t.Setenv("PAYMENT_RETRY_MAX", tt.paymentRetryMax)
			t.Setenv("PAYMENT_PROVIDER_TIMEOUT", tt.paymentProviderTimeout)
			t.Setenv("PAYMENT_PROVIDER_MAX_CONCURRENT", tt.paymentProviderMaxConcurrent)
			t.Setenv("PAYMENT_CIRCUIT_FAILURE_THRESHOLD", tt.paymentCircuitFailureThreshold)
			t.Setenv("PAYMENT_CIRCUIT_OPEN_DURATION", tt.paymentCircuitOpenDuration)
			t.Setenv("SIMULATED_PAYMENT_DELAY", tt.simulatedPaymentDelay)
			t.Setenv("SIMULATED_PAYMENT_FAILURES", tt.simulatedPaymentFailures)
			t.Setenv("MESSAGE_DRIVER", tt.messageDriver)
			t.Setenv("KAFKA_BROKERS", tt.kafkaBrokers)
			t.Setenv("KAFKA_CONSUMER_GROUP", tt.kafkaConsumerGroup)
			t.Setenv("KAFKA_DEAD_LETTER_TOPIC", tt.kafkaDeadLetterTopic)
			t.Setenv("KAFKA_CONSUMER_MAX_ATTEMPTS", tt.kafkaConsumerMaxAttempts)
			t.Setenv("KAFKA_CONSUMER_RETRY", tt.kafkaConsumerRetry)
			t.Setenv("NOTIFICATION_POLL_INTERVAL", tt.notificationPollInterval)
			t.Setenv("NOTIFICATION_BATCH_SIZE", tt.notificationBatchSize)
			t.Setenv("NOTIFICATION_WORKER_CONCURRENCY", tt.notificationWorkerConcurrency)
			t.Setenv("NOTIFICATION_LEASE_DURATION", tt.notificationLeaseDuration)
			t.Setenv("NOTIFICATION_RETRY_BASE", tt.notificationRetryBase)
			t.Setenv("NOTIFICATION_RETRY_MAX", tt.notificationRetryMax)
			t.Setenv("NOTIFICATION_PROVIDER_TIMEOUT", tt.notificationProviderTimeout)
			t.Setenv("NOTIFICATION_WEBHOOK_SECRET", tt.notificationWebhookSecret)
			t.Setenv("NOTIFICATION_WEBHOOK_TOLERANCE", tt.notificationWebhookTolerance)
			t.Setenv("SIMULATED_NOTIFICATION_DELAY", tt.simulatedNotificationDelay)
			t.Setenv("SIMULATED_NOTIFICATION_FAILURES", tt.simulatedNotificationFailures)
			t.Setenv("SIMULATED_NOTIFICATION_REJECTIONS", tt.simulatedNotificationRejections)
			t.Setenv("SIMULATED_NOTIFICATION_RESPONSE_LOSSES", tt.simulatedNotificationResponseLosses)

			got, err := config.Load()

			if tt.wantErrorContains != "" {
				if err == nil {
					t.Fatalf(
						"Load() error = nil, want error containing %q",
						tt.wantErrorContains,
					)
				}

				if !strings.Contains(err.Error(), tt.wantErrorContains) {
					t.Fatalf(
						"Load() error = %q, want error containing %q",
						err,
						tt.wantErrorContains,
					)
				}

				// 错误场景只验证错误，不比较返回的 Config。
				return
			}

			if err != nil {
				t.Fatalf("Load() unexpected error = %v", err)
			}

			want := tt.want
			if want.ShutdownDrainDelay == 0 {
				want.ShutdownDrainDelay = 2 * time.Second
			}
			if want.CacheDriver == "" {
				want.CacheDriver = config.CacheNone
				want.RedisKeyPrefix = "order-system"
				want.CatalogCacheTTL = 5 * time.Minute
				want.CatalogNotFoundCacheTTL = 15 * time.Second
				want.CatalogLoadTimeout = 2 * time.Second
			}
			if want.MessageDriver == "" {
				want.MessageDriver = config.MessageDirect
				want.KafkaConsumerGroup = "order-system-payments-v1"
			}
			if want.KafkaDeadLetterTopic == "" {
				want.KafkaDeadLetterTopic = "orders.created.v1.dlq"
			}
			if want.KafkaConsumerMaxAttempts == 0 {
				want.KafkaConsumerMaxAttempts = 3
			}
			if want.KafkaConsumerRetry == 0 {
				want.KafkaConsumerRetry = time.Second
			}
			if want.PaymentWorkerConcurrency == 0 {
				want.PaymentWorkerConcurrency = 4
			}
			if want.PaymentProviderTimeout == 0 {
				want.PaymentProviderTimeout = 2 * time.Second
			}
			if want.PaymentProviderMaxConcurrent == 0 {
				want.PaymentProviderMaxConcurrent = 4
			}
			if want.PaymentCircuitFailureThreshold == 0 {
				want.PaymentCircuitFailureThreshold = 5
			}
			if want.PaymentCircuitOpenDuration == 0 {
				want.PaymentCircuitOpenDuration = 30 * time.Second
			}
			if want.NotificationPollInterval == 0 {
				want.NotificationPollInterval = time.Second
			}
			if want.NotificationBatchSize == 0 {
				want.NotificationBatchSize = 10
			}
			if want.NotificationWorkerConcurrency == 0 {
				want.NotificationWorkerConcurrency = 4
			}
			if want.NotificationLeaseDuration == 0 {
				want.NotificationLeaseDuration = 30 * time.Second
			}
			if want.NotificationRetryBase == 0 {
				want.NotificationRetryBase = time.Second
			}
			if want.NotificationRetryMax == 0 {
				want.NotificationRetryMax = time.Minute
			}
			if want.NotificationProviderTimeout == 0 {
				want.NotificationProviderTimeout = 2 * time.Second
			}
			if want.NotificationWebhookTolerance == 0 {
				want.NotificationWebhookTolerance = 5 * time.Minute
			}
			if want.CredentialCleanupInterval == 0 {
				want.CredentialCleanupInterval = time.Hour
			}
			if want.CredentialCleanupBatchSize == 0 {
				want.CredentialCleanupBatchSize = 500
			}
			if got != want {
				t.Errorf("Load() = %+v, want %+v", got, want)
			}
		})
	}
}
