package paymentprovider_test

import (
	"context"
	"errors"
	"testing"

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
