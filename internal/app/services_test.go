package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/config"
)

type dependencyPingerStub struct {
	err error
}

func (s dependencyPingerStub) PingContext(context.Context) error {
	return s.err
}

func TestServicesReadiness(t *testing.T) {
	t.Run("memory storage has no external dependency", func(t *testing.T) {
		if err := (&Services{}).CheckReadiness(context.Background()); err != nil {
			t.Fatalf("CheckReadiness() error = %v", err)
		}
	})

	t.Run("reports storage failure", func(t *testing.T) {
		wantErr := errors.New("database unavailable")
		services := &Services{readiness: []dependencyPinger{dependencyPingerStub{err: wantErr}}}

		err := services.CheckReadiness(context.Background())

		if !errors.Is(err, wantErr) {
			t.Fatalf("CheckReadiness() error = %v, want wrapped %v", err, wantErr)
		}
	})

	t.Run("checks dependencies added after storage", func(t *testing.T) {
		wantErr := errors.New("broker unavailable")
		services := &Services{}
		services.AddReadinessDependency(pingContextAdapter{err: wantErr})
		if err := services.CheckReadiness(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("CheckReadiness() error = %v, want wrapped %v", err, wantErr)
		}
	})
}

type pingContextAdapter struct{ err error }

func (s pingContextAdapter) PingContext(context.Context) error { return s.err }

func TestNewMemoryServicesWiresAsyncPaymentFlow(t *testing.T) {
	services, err := NewServices(context.Background(), config.Config{
		StorageDriver:  config.StorageMemory,
		AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, BcryptCost: 4,
	})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	t.Cleanup(func() { _ = services.Close() })
	if services.Payments == nil || services.PaymentTasks == nil || services.PaymentProvider == nil {
		t.Fatal("payment consumer, task store, and provider must be wired")
	}
}
