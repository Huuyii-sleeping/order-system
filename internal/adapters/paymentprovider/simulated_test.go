package paymentprovider_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/paymentprovider"
	paymentapp "example.com/order-system/internal/application/payment"
)

func TestSimulatedProviderIsIdempotent(t *testing.T) {
	provider := paymentprovider.NewSimulated()
	request := paymentapp.ChargeRequest{
		PaymentID: "payment-1", OrderID: "order-1", AmountCents: 12900,
		IdempotencyKey: "payment-1",
	}
	first, err := provider.Charge(context.Background(), request)
	if err != nil {
		t.Fatalf("first Charge() error = %v", err)
	}
	second, err := provider.Charge(context.Background(), request)
	if err != nil || second != first {
		t.Fatalf("second Charge() = %+v, %v; want %+v", second, err, first)
	}

	request.AmountCents++
	if _, err := provider.Charge(context.Background(), request); !errors.Is(err, paymentprovider.ErrIdempotencyConflict) {
		t.Fatalf("conflicting Charge() error = %v", err)
	}
}

func TestSimulatedProviderSupportsDeterministicFaults(t *testing.T) {
	provider := paymentprovider.NewSimulatedWithConfig(paymentprovider.SimulatedConfig{Failures: 1})
	request := paymentapp.ChargeRequest{
		PaymentID: "payment-1", OrderID: "order-1", AmountCents: 12900,
		IdempotencyKey: "payment-1",
	}
	if _, err := provider.Charge(context.Background(), request); err == nil {
		t.Fatal("first Charge() error = nil, want simulated failure")
	}
	if result, err := provider.Charge(context.Background(), request); err != nil ||
		result.Status != paymentapp.ChargeSucceeded {
		t.Fatalf("second Charge() = %+v, %v", result, err)
	}

	slow := paymentprovider.NewSimulatedWithConfig(paymentprovider.SimulatedConfig{Delay: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := slow.Charge(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow Charge() error = %v, want context deadline exceeded", err)
	}
}
