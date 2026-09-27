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
		appName         string
		httpAddr        string
		shutdownTimeout string
		storageDriver   string
		databaseURL     string

		want              config.Config
		wantErrorContains string
	}{
		{
			name:            "uses defaults when environment variables are empty",
			appName:         "",
			httpAddr:        "",
			shutdownTimeout: "",
			want: config.Config{
				AppName:         "order-system",
				HTTPAddr:        ":8080",
				ShutdownTimeout: 10 * time.Second,
				StorageDriver:   config.StorageMemory,
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
				AppName:         "checkout-service",
				HTTPAddr:        ":9090",
				ShutdownTimeout: 30 * time.Second,
				StorageDriver:   config.StoragePostgres,
				DatabaseURL:     "postgres://localhost/order_system",
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
			name:              "rejects unsupported storage driver",
			storageDriver:     "mongodb",
			wantErrorContains: "unsupported STORAGE_DRIVER",
		},
		{
			name:              "postgres requires database URL",
			storageDriver:     config.StoragePostgres,
			wantErrorContains: "DATABASE_URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv 会在子测试结束后自动恢复原来的环境变量。
			// 因此每个用例都不会污染其他用例。
			t.Setenv("APP_NAME", tt.appName)
			t.Setenv("HTTP_ADDR", tt.httpAddr)
			t.Setenv("SHUTDOWN_TIMEOUT", tt.shutdownTimeout)
			t.Setenv("STORAGE_DRIVER", tt.storageDriver)
			t.Setenv("DATABASE_URL", tt.databaseURL)

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
