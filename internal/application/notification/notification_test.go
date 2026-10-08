package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/application/event"
	notificationapp "example.com/order-system/internal/application/notification"
	"example.com/order-system/internal/application/outbox"
	domainnotification "example.com/order-system/internal/domain/notification"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type ids struct{ values []string }

func (g *ids) NewID() (string, error) {
	if len(g.values) == 0 {
		return "", errors.New("no IDs left")
	}
	id := g.values[0]
	g.values = g.values[1:]
	return id, nil
}

func TestConsumerCreatesIdempotentNotificationTask(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: now})
	message := requestedMessage(t, "notification-event-1", "notification-1", now)

	first, err := consumer.Consume(context.Background(), message)
	if err != nil || !first.Processed || first.Notification.Status() != domainnotification.StatusPending {
		t.Fatalf("first Consume() = %+v, %v", first, err)
	}
	second, err := consumer.Consume(context.Background(), message)
	if err != nil || second.Processed || second.Notification.ID() != first.Notification.ID() {
		t.Fatalf("duplicate Consume() = %+v, %v", second, err)
	}
}

func TestWorkerSendsAndRetriesWithStableIdempotencyKey(t *testing.T) {
	createdAt := time.Now().UTC().Add(-time.Second)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: createdAt})
	message := requestedMessage(t, "notification-event-1", "notification-1", createdAt)
	if _, err := consumer.Consume(context.Background(), message); err != nil {
		t.Fatal(err)
	}

	provider := &providerStub{failures: 1}
	worker := notificationapp.NewWorker(store, provider, nil, notificationapp.WorkerConfig{
		BatchSize: 1, LeaseDuration: time.Minute, RetryBase: time.Millisecond, RetryMax: time.Second,
	}, nil)
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("first RunOnce() = %d, %v", processed, err)
	}
	time.Sleep(5 * time.Millisecond)
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("second RunOnce() = %d, %v", processed, err)
	}
	if len(provider.requests) != 2 || provider.requests[0].IdempotencyKey != provider.requests[1].IdempotencyKey {
		t.Fatalf("provider requests = %+v; want stable key", provider.requests)
	}
	got, err := store.GetNotificationByOrderIDAndCustomerID(context.Background(), "order-1", "customer-1")
	if err != nil || got.Status() != domainnotification.StatusSent {
		t.Fatalf("notification = %+v, %v; want sent", got, err)
	}
}

type providerStub struct {
	failures int
	requests []notificationapp.SendRequest
}

func (p *providerStub) Send(_ context.Context, request notificationapp.SendRequest) (notificationapp.SendResult, error) {
	p.requests = append(p.requests, request)
	if p.failures > 0 {
		p.failures--
		return notificationapp.SendResult{}, errors.New("provider unavailable")
	}
	return notificationapp.SendResult{Status: notificationapp.SendSucceeded, ProviderReference: "provider-1"}, nil
}

func requestedMessage(t *testing.T, id, notificationID string, occurredAt time.Time) outbox.Message {
	t.Helper()
	payload, err := json.Marshal(event.NotificationRequestedV1{
		NotificationID: notificationID, PaymentID: "payment-1", OrderID: "order-1",
		CustomerID: "customer-1", RecipientID: "customer-1",
		EventType: event.NotificationPaymentSucceededV1, AmountCents: 12900,
		OccurredAt: occurredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := outbox.NewMessage(id, event.NotificationRequestedV1Topic, "order-1", payload, occurredAt)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
