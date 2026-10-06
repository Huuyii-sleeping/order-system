package redis

import (
	"context"
	"errors"
	"testing"
	"time"
)

type counterStub struct {
	count     int64
	remaining time.Duration
	err       error
	key       string
}

func (s *counterStub) IncrementWindow(
	_ context.Context, key string, _ time.Duration,
) (int64, time.Duration, error) {
	s.key = key
	return s.count, s.remaining, s.err
}

func TestFixedWindowRateLimiter(t *testing.T) {
	counter := &counterStub{count: 3, remaining: 12 * time.Second}
	limiter := NewFixedWindowRateLimiter(counter, "test:rate", 2, time.Minute)

	allowed, retryAfter, err := limiter.Allow(context.Background(), "192.0.2.10")
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if allowed || retryAfter != 12*time.Second {
		t.Fatalf("Allow() = %v, %v; want false, 12s", allowed, retryAfter)
	}
	if counter.key == "test:rate:192.0.2.10" {
		t.Fatal("rate-limit key exposed the raw client address")
	}
}

func TestFixedWindowRateLimiterReturnsStoreError(t *testing.T) {
	wantErr := errors.New("redis unavailable")
	limiter := NewFixedWindowRateLimiter(
		&counterStub{err: wantErr}, "test:rate", 2, time.Minute,
	)
	_, _, err := limiter.Allow(context.Background(), "192.0.2.10")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Allow() error = %v, want %v", err, wantErr)
	}
}
