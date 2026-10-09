package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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

func TestWebhookCompletesNotificationAndTaskIdempotently(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: now.Add(-time.Minute)})
	if _, err := consumer.Consume(context.Background(), requestedMessage(t, "notification-event-1", "notification-1", now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	service := notificationapp.NewWebhookService(store, fixedClock{now: now})
	claimed, err := store.ClaimTasks(context.Background(), "worker-before-webhook", 1, now.Add(-time.Second), now.Add(time.Minute))
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimTasks() before webhook = %+v, %v", claimed, err)
	}
	event := notificationapp.WebhookEvent{
		ID: "webhook-1", NotificationID: "notification-1", IdempotencyKey: "notification-1",
		Status: notificationapp.SendSucceeded, ProviderReference: "provider-reference-1",
		PayloadHash: strings.Repeat("a", 64),
	}

	first, err := service.Process(context.Background(), event)
	if err != nil || !first.Processed || first.Notification.Status() != domainnotification.StatusSent {
		t.Fatalf("first Process() = %+v, %v", first, err)
	}
	second, err := service.Process(context.Background(), event)
	if err != nil || second.Processed || second.Notification.Status() != domainnotification.StatusSent {
		t.Fatalf("duplicate Process() = %+v, %v", second, err)
	}
	staleResult, err := claimed[0].Notification.MarkSent("provider-reference-1", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteTask(context.Background(), claimed[0].ID, claimed[0].LeaseID, notificationapp.TaskOutcome{Notification: staleResult}, now.Add(time.Second)); !errors.Is(err, notificationapp.ErrTaskLeaseLost) {
		t.Fatalf("stale worker CompleteTask() error = %v, want lease lost", err)
	}
	remaining, err := store.ClaimTasks(context.Background(), "lease-after-webhook", 1, now.Add(time.Hour), now.Add(2*time.Hour))
	if err != nil || len(remaining) != 0 {
		t.Fatalf("ClaimTasks() after webhook = %+v, %v; want no pending task", remaining, err)
	}
}

func TestWebhookRejectsChangedDuplicateAndOppositeTerminalResult(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: now.Add(-time.Minute)})
	if _, err := consumer.Consume(context.Background(), requestedMessage(t, "notification-event-1", "notification-1", now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	service := notificationapp.NewWebhookService(store, fixedClock{now: now})
	succeeded := notificationapp.WebhookEvent{
		ID: "webhook-1", NotificationID: "notification-1", IdempotencyKey: "notification-1",
		Status: notificationapp.SendSucceeded, ProviderReference: "provider-reference-1",
		PayloadHash: strings.Repeat("a", 64),
	}
	if _, err := service.Process(context.Background(), succeeded); err != nil {
		t.Fatal(err)
	}
	changed := succeeded
	changed.PayloadHash = strings.Repeat("b", 64)
	if _, err := service.Process(context.Background(), changed); !errors.Is(err, notificationapp.ErrWebhookEventConflict) {
		t.Fatalf("changed duplicate error = %v, want conflict", err)
	}
	rejected := notificationapp.WebhookEvent{
		ID: "webhook-2", NotificationID: "notification-1", IdempotencyKey: "notification-1",
		Status: notificationapp.SendRejected, Reason: "provider rejected delivery",
		PayloadHash: strings.Repeat("c", 64),
	}
	if _, err := service.Process(context.Background(), rejected); !errors.Is(err, notificationapp.ErrWebhookResultConflict) {
		t.Fatalf("opposite terminal result error = %v, want conflict", err)
	}
	// 冲突事务不会留下“已处理”标记；同一个 event ID 修正为与终态一致的
	// 内容后仍可正常消费，证明事件记录和状态更新共享事务。
	corrected := succeeded
	corrected.ID = rejected.ID
	corrected.PayloadHash = strings.Repeat("d", 64)
	if result, err := service.Process(context.Background(), corrected); err != nil || !result.Processed {
		t.Fatalf("corrected event Process() = %+v, %v", result, err)
	}
	got, err := store.GetNotificationByOrderIDAndCustomerID(context.Background(), "order-1", "customer-1")
	if err != nil || got.Status() != domainnotification.StatusSent {
		t.Fatalf("notification after conflicts = %+v, %v; want sent", got, err)
	}
}

func TestWebhookRejectsMismatchedIdempotencyKey(t *testing.T) {
	now := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: now.Add(-time.Minute)})
	if _, err := consumer.Consume(context.Background(), requestedMessage(t, "notification-event-1", "notification-1", now.Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	service := notificationapp.NewWebhookService(store, fixedClock{now: now})
	_, err := service.Process(context.Background(), notificationapp.WebhookEvent{
		ID: "webhook-1", NotificationID: "notification-1", IdempotencyKey: "different-key",
		Status: notificationapp.SendSucceeded, ProviderReference: "provider-reference-1",
		PayloadHash: strings.Repeat("a", 64),
	})
	if !errors.Is(err, domainnotification.ErrNotificationNotFound) {
		t.Fatalf("Process() error = %v, want notification not found", err)
	}
	got, err := store.GetNotificationByOrderIDAndCustomerID(context.Background(), "order-1", "customer-1")
	if err != nil || got.Status() != domainnotification.StatusPending {
		t.Fatalf("notification = %+v, %v; want pending", got, err)
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

func TestWorkerReconcilesLostProviderResponseWithoutSendingTwice(t *testing.T) {
	createdAt := time.Now().UTC().Add(-time.Second)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: createdAt})
	if _, err := consumer.Consume(context.Background(), requestedMessage(t, "notification-event-1", "notification-1", createdAt)); err != nil {
		t.Fatal(err)
	}
	provider := &providerStub{loseResponseAfterSend: true}
	worker := notificationapp.NewWorker(store, provider, nil, notificationapp.WorkerConfig{
		BatchSize: 1, LeaseDuration: time.Minute, RetryBase: time.Millisecond,
		RetryMax: time.Second, ProviderTimeout: time.Second,
	}, nil)
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("first RunOnce() = %d, %v", processed, err)
	}
	unknown, err := store.GetNotificationByOrderIDAndCustomerID(context.Background(), "order-1", "customer-1")
	if err != nil || unknown.Status() != domainnotification.StatusUnknown {
		t.Fatalf("notification after lost response = %+v, %v; want unknown", unknown, err)
	}
	time.Sleep(5 * time.Millisecond)
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("second RunOnce() = %d, %v", processed, err)
	}
	got, err := store.GetNotificationByOrderIDAndCustomerID(context.Background(), "order-1", "customer-1")
	if err != nil || got.Status() != domainnotification.StatusSent {
		t.Fatalf("notification after reconciliation = %+v, %v; want sent", got, err)
	}
	if len(provider.requests) != 1 || provider.lookups != 1 {
		t.Fatalf("provider sends/lookups = %d/%d, want 1/1", len(provider.requests), provider.lookups)
	}
}

func TestWorkerDoesNotSendWhenReconciliationLookupFails(t *testing.T) {
	createdAt := time.Now().UTC().Add(-time.Second)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: createdAt})
	if _, err := consumer.Consume(context.Background(), requestedMessage(t, "notification-event-1", "notification-1", createdAt)); err != nil {
		t.Fatal(err)
	}
	provider := &providerStub{failures: 1, lookupFailures: 1}
	worker := notificationapp.NewWorker(store, provider, nil, notificationapp.WorkerConfig{
		BatchSize: 1, LeaseDuration: time.Minute, RetryBase: time.Millisecond,
		RetryMax: time.Second, ProviderTimeout: time.Second,
	}, nil)
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNotificationByOrderIDAndCustomerID(context.Background(), "order-1", "customer-1")
	if err != nil || got.Status() != domainnotification.StatusUnknown {
		t.Fatalf("notification = %+v, %v; want unknown", got, err)
	}
	if len(provider.requests) != 1 || provider.lookups != 1 {
		t.Fatalf("provider sends/lookups = %d/%d, want 1/1", len(provider.requests), provider.lookups)
	}
}

func TestWorkerReconcilesExpiredProcessingLease(t *testing.T) {
	createdAt := time.Now().UTC().Add(-2 * time.Second)
	store := memory.NewNotificationStore()
	consumer := notificationapp.NewConsumer(store, &ids{values: []string{"task-1"}}, fixedClock{now: createdAt})
	if _, err := consumer.Consume(context.Background(), requestedMessage(t, "notification-event-1", "notification-1", createdAt)); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimTasks(context.Background(), "crashed-worker", 1, createdAt, createdAt.Add(time.Second))
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimTasks() = %+v, %v; want one task", claimed, err)
	}
	provider := &providerStub{results: map[string]notificationapp.SendResult{
		"notification-1": {Status: notificationapp.SendSucceeded, ProviderReference: "provider-1"},
	}}
	worker := notificationapp.NewWorker(store, provider, nil, notificationapp.WorkerConfig{
		BatchSize: 1, LeaseDuration: time.Minute, ProviderTimeout: time.Second,
	}, nil)
	if processed, err := worker.RunOnce(context.Background()); err != nil || processed != 1 {
		t.Fatalf("RunOnce() = %d, %v", processed, err)
	}
	got, err := store.GetNotificationByOrderIDAndCustomerID(context.Background(), "order-1", "customer-1")
	if err != nil || got.Status() != domainnotification.StatusSent {
		t.Fatalf("notification = %+v, %v; want sent", got, err)
	}
	if len(provider.requests) != 0 || provider.lookups != 1 {
		t.Fatalf("provider sends/lookups = %d/%d, want 0/1", len(provider.requests), provider.lookups)
	}
}

type providerStub struct {
	failures              int
	loseResponseAfterSend bool
	requests              []notificationapp.SendRequest
	results               map[string]notificationapp.SendResult
	lookups               int
	lookupFailures        int
}

func (p *providerStub) Send(_ context.Context, request notificationapp.SendRequest) (notificationapp.SendResult, error) {
	p.requests = append(p.requests, request)
	if p.failures > 0 {
		p.failures--
		return notificationapp.SendResult{}, errors.New("provider unavailable")
	}
	result := notificationapp.SendResult{Status: notificationapp.SendSucceeded, ProviderReference: "provider-1"}
	if p.results == nil {
		p.results = make(map[string]notificationapp.SendResult)
	}
	p.results[request.IdempotencyKey] = result
	if p.loseResponseAfterSend {
		p.loseResponseAfterSend = false
		return notificationapp.SendResult{}, errors.New("response lost after send")
	}
	return result, nil
}

func (p *providerStub) Lookup(_ context.Context, idempotencyKey string) (notificationapp.LookupResult, error) {
	p.lookups++
	if p.lookupFailures > 0 {
		p.lookupFailures--
		return notificationapp.LookupResult{}, errors.New("provider lookup unavailable")
	}
	result, found := p.results[idempotencyKey]
	return notificationapp.LookupResult{Found: found, Result: result}, nil
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
