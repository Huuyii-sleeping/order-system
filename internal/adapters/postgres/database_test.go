package postgres

import (
	"strings"
	"testing"
	"time"
)

func TestPoolOptionsValidate(t *testing.T) {
	valid := DefaultPoolOptions()
	for _, tt := range []struct {
		name    string
		mutate  func(*PoolOptions)
		wantErr string
	}{
		{name: "valid defaults", mutate: func(*PoolOptions) {}},
		{name: "zero max open", mutate: func(options *PoolOptions) { options.MaxOpenConns = 0 }, wantErr: "max open"},
		{name: "zero max idle", mutate: func(options *PoolOptions) { options.MaxIdleConns = 0 }, wantErr: "max idle"},
		{name: "idle exceeds open", mutate: func(options *PoolOptions) { options.MaxIdleConns = options.MaxOpenConns + 1 }, wantErr: "must not exceed"},
		{name: "zero lifetime", mutate: func(options *PoolOptions) { options.ConnMaxLifetime = 0 }, wantErr: "max lifetime"},
		{name: "zero idle time", mutate: func(options *PoolOptions) { options.ConnMaxIdleTime = 0 }, wantErr: "max idle time"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options := valid
			tt.mutate(&options)
			err := options.validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("validate() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("validate() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultPoolOptionsAreBounded(t *testing.T) {
	options := DefaultPoolOptions()
	if options.MaxOpenConns != 20 || options.MaxIdleConns != 5 {
		t.Fatalf("default connection limits = %+v", options)
	}
	if options.ConnMaxLifetime != 30*time.Minute || options.ConnMaxIdleTime != 5*time.Minute {
		t.Fatalf("default connection ages = %+v", options)
	}
}
