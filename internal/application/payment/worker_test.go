package paymentapp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/application/event"
	"example.com/order-system/internal/domain/payment"
)

type taskStoreStub struct {
	tasks       []Task
	completed   TaskOutcome
	retried     TaskOutcome
	nextAttempt time.Time
}

func (s *taskStoreStub) ClaimTasks(
	_ context.Context, leaseID string, _ int, _ time.Time, _ time.Time,
) ([]Task, error) {
	tasks := append([]Task(nil), s.tasks...)
	for index := range tasks {
		tasks[index].LeaseID = leaseID
		tasks[index].Attempts++
		processing, err := tasks[index].Payment.StartProcessing(tasks[index].Payment.UpdatedAt())
		if err != nil {
			return nil, err
		}
		tasks[index].Payment = processing
	}
	return tasks, nil
}

func (s *taskStoreStub) CompleteTask(
	_ context.Context, _, _ string, outcome TaskOutcome, _ time.Time,
) error {
	s.completed = outcome
	return nil
}

func (s *taskStoreStub) RetryTask(
	_ context.Context, _, _ string, outcome TaskOutcome, nextAttempt time.Time,
) error {
	s.retried = outcome
	s.nextAttempt = nextAttempt
	return nil
}

type providerStub struct {
	result   ChargeResult
	err      error
	requests []ChargeRequest
}

func (p *providerStub) Charge(_ context.Context, request ChargeRequest) (ChargeResult, error) {
	p.requests = append(p.requests, request)
	return p.result, p.err
}

func TestWorkerCompletesSuccessfulCharge(t *testing.T) {
	store := &taskStoreStub{tasks: []Task{testTask(t)}}
	provider := &providerStub{result: ChargeResult{Status: ChargeSucceeded, ProviderReference: "provider-1"}}
	worker := NewWorker(store, provider, nil, WorkerConfig{})
	worker.newID = sequenceWorkerIDs("lease-1", "event-1")
	worker.now = testWorkerClock()

	processed, err := worker.RunOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("RunOnce() = %d, %v", processed, err)
	}
	if store.completed.Payment.Status() != payment.StatusSucceeded ||
		store.completed.Payment.ProviderReference() != "provider-1" {
		t.Fatalf("completed outcome = %+v", store.completed)
	}
	if len(provider.requests) != 1 || provider.requests[0].IdempotencyKey != "payment-1" {
		t.Fatalf("provider requests = %+v", provider.requests)
	}
	if store.completed.ResultMessage.Topic != event.PaymentSucceededV1Topic ||
		store.completed.ResultMessage.Key != "order-1" {
		t.Fatalf("result message = %+v", store.completed.ResultMessage)
	}
	var succeeded event.PaymentSucceededV1
	if err := json.Unmarshal(store.completed.ResultMessage.Payload, &succeeded); err != nil {
		t.Fatalf("decode succeeded event: %v", err)
	}
	if succeeded.PaymentID != "payment-1" || succeeded.OrderID != "order-1" ||
		succeeded.CustomerID != "customer-1" || succeeded.AmountCents != 12900 {
		t.Fatalf("succeeded event = %+v", succeeded)
	}
}

func TestWorkerRecordsUnknownAndRetriesProviderError(t *testing.T) {
	store := &taskStoreStub{tasks: []Task{testTask(t)}}
	provider := &providerStub{err: errors.New("provider timeout")}
	worker := NewWorker(store, provider, nil, WorkerConfig{RetryBase: 2 * time.Second, RetryMax: time.Minute})
	worker.newID = func() (string, error) { return "lease-1", nil }
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return now }

	processed, err := worker.RunOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("RunOnce() = %d, %v", processed, err)
	}
	if store.retried.Payment.Status() != payment.StatusUnknown ||
		store.retried.Reason != "provider timeout" ||
		!store.nextAttempt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("retry outcome = %+v, next = %s", store.retried, store.nextAttempt)
	}
	if store.retried.ResultMessage.ID != "" {
		t.Fatalf("unknown result must not publish an event: %+v", store.retried.ResultMessage)
	}
}

func TestWorkerCompletesDeclinedCharge(t *testing.T) {
	store := &taskStoreStub{tasks: []Task{testTask(t)}}
	provider := &providerStub{result: ChargeResult{Status: ChargeDeclined, Reason: "card declined"}}
	worker := NewWorker(store, provider, nil, WorkerConfig{})
	worker.newID = sequenceWorkerIDs("lease-1", "event-1")
	worker.now = testWorkerClock()

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if store.completed.Payment.Status() != payment.StatusFailed || store.completed.Reason != "card declined" {
		t.Fatalf("completed outcome = %+v", store.completed)
	}
	if store.completed.ResultMessage.Topic != event.PaymentFailedV1Topic {
		t.Fatalf("result message topic = %q, want %q",
			store.completed.ResultMessage.Topic, event.PaymentFailedV1Topic)
	}
	if string(store.completed.ResultMessage.Payload) == "" ||
		json.Valid(store.completed.ResultMessage.Payload) == false {
		t.Fatalf("failed event payload = %q", store.completed.ResultMessage.Payload)
	}
	if strings.Contains(string(store.completed.ResultMessage.Payload), "card declined") {
		t.Fatalf("failed event leaks provider reason: %s", store.completed.ResultMessage.Payload)
	}
}

func sequenceWorkerIDs(values ...string) func() (string, error) {
	index := 0
	return func() (string, error) {
		value := values[index]
		index++
		return value, nil
	}
}

func testTask(t *testing.T) Task {
	t.Helper()
	created, err := payment.New("payment-1", "order-1", "customer-1", 12900,
		time.Date(2026, 9, 29, 7, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("payment.New() error = %v", err)
	}
	task, err := NewTask("task-1", created)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	return task
}

func testWorkerClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC) }
}
