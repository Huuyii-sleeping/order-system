package paymentapp

import (
	"context"
	"errors"
	"testing"
	"time"

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
	worker.newID = func() (string, error) { return "lease-1", nil }
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
}

func TestWorkerCompletesDeclinedCharge(t *testing.T) {
	store := &taskStoreStub{tasks: []Task{testTask(t)}}
	provider := &providerStub{result: ChargeResult{Status: ChargeDeclined, Reason: "card declined"}}
	worker := NewWorker(store, provider, nil, WorkerConfig{})
	worker.newID = func() (string, error) { return "lease-1", nil }
	worker.now = testWorkerClock()

	if _, err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if store.completed.Payment.Status() != payment.StatusFailed || store.completed.Reason != "card declined" {
		t.Fatalf("completed outcome = %+v", store.completed)
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
