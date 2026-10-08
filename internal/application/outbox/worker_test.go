package outbox

import (
	"context"
	"errors"
	"testing"
	"time"
)

type storeStub struct {
	messages       []Message
	claimLeaseID   string
	publishedID    string
	publishedLease string
	failedID       string
	failedLease    string
	nextAttemptAt  time.Time
	failureReason  string
}

func (s *storeStub) Claim(
	_ context.Context, leaseID string, _ int, _ time.Time, _ time.Time,
) ([]Message, error) {
	s.claimLeaseID = leaseID
	messages := append([]Message(nil), s.messages...)
	for index := range messages {
		messages[index].LeaseID = leaseID
		messages[index].Attempts++
	}
	return messages, nil
}

func (s *storeStub) MarkPublished(
	_ context.Context, messageID, leaseID string, _ time.Time,
) error {
	s.publishedID = messageID
	s.publishedLease = leaseID
	return nil
}

func (s *storeStub) MarkFailed(
	_ context.Context, messageID, leaseID string, nextAttemptAt time.Time, reason string,
) error {
	s.failedID = messageID
	s.failedLease = leaseID
	s.nextAttemptAt = nextAttemptAt
	s.failureReason = reason
	return nil
}

type publisherStub struct {
	err       error
	published []Message
}

type blockingPublisher struct {
	started chan struct{}
	finish  chan struct{}
}

func (p *blockingPublisher) Publish(ctx context.Context, _ Message) error {
	close(p.started)
	select {
	case <-p.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *publisherStub) Publish(_ context.Context, message Message) error {
	p.published = append(p.published, message)
	return p.err
}

func TestWorkerMarksPublishedMessageWithLease(t *testing.T) {
	store := &storeStub{messages: []Message{{ID: "message-1", Topic: "orders.created.v1"}}}
	publisher := &publisherStub{}
	worker := NewWorker(store, publisher, nil, WorkerConfig{})
	worker.newID = func() (string, error) { return "lease-1", nil }
	worker.now = func() time.Time { return time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC) }

	processed, err := worker.RunOnce(context.Background())

	if err != nil || processed != 1 {
		t.Fatalf("RunOnce() = %d, %v; want 1, nil", processed, err)
	}
	if len(publisher.published) != 1 || store.publishedID != "message-1" ||
		store.publishedLease != "lease-1" {
		t.Fatalf("published = %+v, confirmation = %q/%q",
			publisher.published, store.publishedID, store.publishedLease)
	}
}

func TestWorkerSchedulesFailedMessageForRetry(t *testing.T) {
	store := &storeStub{messages: []Message{{ID: "message-1", Topic: "orders.created.v1"}}}
	publisher := &publisherStub{err: errors.New("broker unavailable")}
	worker := NewWorker(store, publisher, nil, WorkerConfig{
		RetryBase: 2 * time.Second, RetryMax: time.Minute,
	})
	worker.newID = func() (string, error) { return "lease-1", nil }
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return now }

	processed, err := worker.RunOnce(context.Background())

	if err != nil || processed != 1 {
		t.Fatalf("RunOnce() = %d, %v; want 1, nil", processed, err)
	}
	if store.failedID != "message-1" || store.failedLease != "lease-1" {
		t.Fatalf("failure confirmation = %q/%q", store.failedID, store.failedLease)
	}
	if want := now.Add(2 * time.Second); !store.nextAttemptAt.Equal(want) {
		t.Errorf("next attempt = %s, want %s", store.nextAttemptAt, want)
	}
	if store.failureReason != "broker unavailable" || store.publishedID != "" {
		t.Errorf("failure reason = %q, published ID = %q", store.failureReason, store.publishedID)
	}
}

func TestWorkerRetryDelayIsCapped(t *testing.T) {
	worker := NewWorker(nil, nil, nil, WorkerConfig{
		RetryBase: time.Second, RetryMax: 8 * time.Second,
	})
	if got := worker.retryDelay(20); got != 8*time.Second {
		t.Errorf("retryDelay(20) = %s, want 8s", got)
	}
}

func TestWorkerGracefulStopFinishesClaimedBatch(t *testing.T) {
	store := &storeStub{messages: []Message{{ID: "message-1", Topic: "orders.created.v1"}}}
	publisher := &blockingPublisher{started: make(chan struct{}), finish: make(chan struct{})}
	worker := NewWorker(store, publisher, nil, WorkerConfig{PollInterval: time.Hour})
	worker.newID = func() (string, error) { return "lease-1", nil }
	stopCtx, stop := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	done := make(chan struct{})
	go func() {
		worker.RunGracefully(stopCtx, workCtx)
		close(done)
	}()
	<-publisher.started

	stop()
	select {
	case <-done:
		t.Fatal("worker stopped before claimed message finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(publisher.finish)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after claimed message finished")
	}
	if store.publishedID != "message-1" {
		t.Fatalf("published ID = %q, want message-1", store.publishedID)
	}
}
