package authapp_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	authapp "example.com/order-system/internal/application/auth"
)

type cleanupClockStub struct{ now time.Time }

func (c cleanupClockStub) Now() time.Time { return c.now }

type cleanupStub struct {
	mu        sync.Mutex
	results   []authapp.CleanupResult
	errors    []error
	called    chan struct{}
	callCount int
}

func (s *cleanupStub) PruneExpiredCredentials(context.Context, time.Time, int) (authapp.CleanupResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount++
	if s.called != nil {
		select {
		case s.called <- struct{}{}:
		default:
		}
	}
	index := s.callCount - 1
	var result authapp.CleanupResult
	if len(s.results) > 0 {
		if index >= len(s.results) {
			index = len(s.results) - 1
		}
		result = s.results[index]
	}
	if len(s.errors) > 0 {
		if index >= len(s.errors) {
			index = len(s.errors) - 1
		}
		return result, s.errors[index]
	}
	return result, nil
}

func TestCleanupWorkerRunsImmediatelyAndOnSchedule(t *testing.T) {
	called := make(chan struct{}, 3)
	cleaner := &cleanupStub{called: called}
	worker := authapp.NewCleanupWorker(
		cleaner,
		cleanupClockStub{now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)},
		nil,
		authapp.CleanupWorkerConfig{Interval: 10 * time.Millisecond, BatchSize: 2},
	)
	stopCtx, stop := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.RunGracefully(stopCtx, workCtx)
		close(done)
	}()

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for immediate cleanup")
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scheduled cleanup")
	}

	stop()
	cancelWork()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop")
	}
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	if cleaner.callCount < 2 {
		t.Fatalf("cleanup call count = %d, want at least 2", cleaner.callCount)
	}
}

func TestCleanupWorkerKeepsRetryingAfterTransientFailure(t *testing.T) {
	called := make(chan struct{}, 2)
	cleaner := &cleanupStub{
		called:  called,
		errors:  []error{errors.New("database unavailable"), nil},
		results: []authapp.CleanupResult{{}, {SessionsDeleted: 1}},
	}
	worker := authapp.NewCleanupWorker(
		cleaner,
		cleanupClockStub{now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)},
		nil,
		authapp.CleanupWorkerConfig{Interval: 10 * time.Millisecond, BatchSize: 2},
	)
	stopCtx, stop := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.RunGracefully(stopCtx, workCtx)
		close(done)
	}()

	for range 2 {
		select {
		case <-called:
		case <-time.After(time.Second):
			stop()
			cancelWork()
			t.Fatal("timed out waiting for cleanup retry")
		}
	}
	stop()
	cancelWork()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup worker did not stop after transient failure")
	}

	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	if cleaner.callCount != 2 {
		t.Fatalf("cleanup call count = %d, want 2", cleaner.callCount)
	}
}
