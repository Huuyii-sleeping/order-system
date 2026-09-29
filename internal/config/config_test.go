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
		appName             string
		httpAddr            string
		shutdownTimeout     string
		readinessTimeout    string
		storageDriver       string
		databaseURL         string
		accessTokenTTL      string
		refreshTokenTTL     string
		secureCookies       string
		authRateWindow      string
		loginRateLimit      string
		refreshRateLimit    string
		outboxPollInterval  string
		outboxBatchSize     string
		outboxLeaseDuration string
		outboxRetryBase     string
		outboxRetryMax      string

		want              config.Config
		wantErrorContains string
	}{
		{
			name:            "uses defaults when environment variables are empty",
			appName:         "",
			httpAddr:        "",
			shutdownTimeout: "",
			want: config.Config{
				AppName:             "order-system",
				HTTPAddr:            ":8080",
				ShutdownTimeout:     10 * time.Second,
				ReadinessTimeout:    2 * time.Second,
				StorageDriver:       config.StorageMemory,
				AccessTokenTTL:      15 * time.Minute,
				RefreshTokenTTL:     30 * 24 * time.Hour,
				AuthRateWindow:      time.Minute,
				LoginRateLimit:      10,
				RefreshRateLimit:    30,
				BcryptCost:          12,
				OutboxPollInterval:  time.Second,
				OutboxBatchSize:     20,
				OutboxLeaseDuration: 30 * time.Second,
				OutboxRetryBase:     time.Second,
				OutboxRetryMax:      time.Minute,
			},
		},
		{
			name:            "environment variables override token lifetimes",
			accessTokenTTL:  "5m",
			refreshTokenTTL: "12h",
			want: config.Config{
				AppName:             "order-system",
				HTTPAddr:            ":8080",
				ShutdownTimeout:     10 * time.Second,
				ReadinessTimeout:    2 * time.Second,
				StorageDriver:       config.StorageMemory,
				AccessTokenTTL:      5 * time.Minute,
				RefreshTokenTTL:     12 * time.Hour,
				AuthRateWindow:      time.Minute,
				LoginRateLimit:      10,
				RefreshRateLimit:    30,
				BcryptCost:          12,
				OutboxPollInterval:  time.Second,
				OutboxBatchSize:     20,
				OutboxLeaseDuration: 30 * time.Second,
				OutboxRetryBase:     time.Second,
				OutboxRetryMax:      time.Minute,
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
				AppName:             "checkout-service",
				HTTPAddr:            ":9090",
				ShutdownTimeout:     30 * time.Second,
				ReadinessTimeout:    2 * time.Second,
				StorageDriver:       config.StoragePostgres,
				DatabaseURL:         "postgres://localhost/order_system",
				AccessTokenTTL:      15 * time.Minute,
				RefreshTokenTTL:     30 * 24 * time.Hour,
				AuthRateWindow:      time.Minute,
				LoginRateLimit:      10,
				RefreshRateLimit:    30,
				BcryptCost:          12,
				OutboxPollInterval:  time.Second,
				OutboxBatchSize:     20,
				OutboxLeaseDuration: 30 * time.Second,
				OutboxRetryBase:     time.Second,
				OutboxRetryMax:      time.Minute,
			},
		},
		{
			name:             "environment variables override auth security",
			secureCookies:    "true",
			authRateWindow:   "30s",
			loginRateLimit:   "4",
			refreshRateLimit: "12",
			want: config.Config{
				AppName:             "order-system",
				HTTPAddr:            ":8080",
				ShutdownTimeout:     10 * time.Second,
				ReadinessTimeout:    2 * time.Second,
				StorageDriver:       config.StorageMemory,
				AccessTokenTTL:      15 * time.Minute,
				RefreshTokenTTL:     30 * 24 * time.Hour,
				SecureCookies:       true,
				AuthRateWindow:      30 * time.Second,
				LoginRateLimit:      4,
				RefreshRateLimit:    12,
				BcryptCost:          12,
				OutboxPollInterval:  time.Second,
				OutboxBatchSize:     20,
				OutboxLeaseDuration: 30 * time.Second,
				OutboxRetryBase:     time.Second,
				OutboxRetryMax:      time.Minute,
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
				AppName:             "order-system",
				HTTPAddr:            ":8080",
				ShutdownTimeout:     10 * time.Second,
				ReadinessTimeout:    2 * time.Second,
				StorageDriver:       config.StorageMemory,
				AccessTokenTTL:      15 * time.Minute,
				RefreshTokenTTL:     30 * 24 * time.Hour,
				AuthRateWindow:      time.Minute,
				LoginRateLimit:      10,
				RefreshRateLimit:    30,
				BcryptCost:          12,
				OutboxPollInterval:  250 * time.Millisecond,
				OutboxBatchSize:     5,
				OutboxLeaseDuration: 10 * time.Second,
				OutboxRetryBase:     2 * time.Second,
				OutboxRetryMax:      30 * time.Second,
			},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv 会在子测试结束后自动恢复原来的环境变量。
			// 因此每个用例都不会污染其他用例。
			t.Setenv("APP_NAME", tt.appName)
			t.Setenv("HTTP_ADDR", tt.httpAddr)
			t.Setenv("SHUTDOWN_TIMEOUT", tt.shutdownTimeout)
			t.Setenv("READINESS_TIMEOUT", tt.readinessTimeout)
			t.Setenv("STORAGE_DRIVER", tt.storageDriver)
			t.Setenv("DATABASE_URL", tt.databaseURL)
			t.Setenv("ACCESS_TOKEN_TTL", tt.accessTokenTTL)
			t.Setenv("REFRESH_TOKEN_TTL", tt.refreshTokenTTL)
			t.Setenv("SECURE_COOKIES", tt.secureCookies)
			t.Setenv("AUTH_RATE_WINDOW", tt.authRateWindow)
			t.Setenv("LOGIN_RATE_LIMIT", tt.loginRateLimit)
			t.Setenv("REFRESH_RATE_LIMIT", tt.refreshRateLimit)
			t.Setenv("OUTBOX_POLL_INTERVAL", tt.outboxPollInterval)
			t.Setenv("OUTBOX_BATCH_SIZE", tt.outboxBatchSize)
			t.Setenv("OUTBOX_LEASE_DURATION", tt.outboxLeaseDuration)
			t.Setenv("OUTBOX_RETRY_BASE", tt.outboxRetryBase)
			t.Setenv("OUTBOX_RETRY_MAX", tt.outboxRetryMax)

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

			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
