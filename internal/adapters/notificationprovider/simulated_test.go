package notificationprovider_test

import (
	"context"
	"errors"
	"testing"

	"example.com/order-system/internal/adapters/notificationprovider"
	notificationapp "example.com/order-system/internal/application/notification"
)

func TestSimulatedLookupRecoversLostResponse(t *testing.T) {
	provider := notificationprovider.NewSimulatedWithConfig(notificationprovider.Config{ResponseLosses: 1})
	request := notificationapp.SendRequest{
		NotificationID: "notification-1", PaymentID: "payment-1", OrderID: "order-1",
		RecipientID: "customer-1", EventType: "payment_succeeded", AmountCents: 12900,
		IdempotencyKey: "notification-1",
	}
	if _, err := provider.Send(context.Background(), request); !errors.Is(err, notificationprovider.ErrResponseLost) {
		t.Fatalf("Send() error = %v, want %v", err, notificationprovider.ErrResponseLost)
	}
	lookup, err := provider.Lookup(context.Background(), request.IdempotencyKey)
	if err != nil || !lookup.Found || lookup.Result.Status != notificationapp.SendSucceeded {
		t.Fatalf("Lookup() = %+v, %v; want succeeded result", lookup, err)
	}
	if lookup.Result.ProviderReference == "" {
		t.Fatal("Lookup() succeeded without provider reference")
	}
}

func TestSimulatedLookupReturnsNotFoundBeforeSend(t *testing.T) {
	provider := notificationprovider.NewSimulated()
	lookup, err := provider.Lookup(context.Background(), "notification-missing")
	if err != nil || lookup.Found {
		t.Fatalf("Lookup() = %+v, %v; want not found", lookup, err)
	}
}

func TestSimulatedLookupReturnsRecordedRejection(t *testing.T) {
	provider := notificationprovider.NewSimulatedWithConfig(notificationprovider.Config{Rejections: 1})
	request := notificationapp.SendRequest{
		NotificationID: "notification-1", PaymentID: "payment-1", OrderID: "order-1",
		RecipientID: "customer-1", EventType: "payment_succeeded", AmountCents: 12900,
		IdempotencyKey: "notification-1",
	}
	result, err := provider.Send(context.Background(), request)
	if err != nil || result.Status != notificationapp.SendRejected {
		t.Fatalf("Send() = %+v, %v; want rejected", result, err)
	}
	lookup, err := provider.Lookup(context.Background(), request.IdempotencyKey)
	if err != nil || !lookup.Found || lookup.Result.Status != notificationapp.SendRejected {
		t.Fatalf("Lookup() = %+v, %v; want recorded rejection", lookup, err)
	}
}
