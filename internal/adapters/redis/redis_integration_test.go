package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/domain/catalog"
)

func TestIntegrationRedisSharesCatalogCacheAcrossInstances(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("TEST_REDIS_URL"))
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstClient, err := Open(ctx, redisURL)
	if err != nil {
		t.Fatalf("open first Redis client: %v", err)
	}
	t.Cleanup(func() { _ = firstClient.Close() })
	secondClient, err := Open(ctx, redisURL)
	if err != nil {
		t.Fatalf("open second Redis client: %v", err)
	}
	t.Cleanup(func() { _ = secondClient.Close() })

	prefix := fmt.Sprintf("integration:%d", time.Now().UnixNano())
	product, err := catalog.NewProduct("shared-product", "Shared Keyboard", 12900)
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	firstSource := &catalogSourceStub{product: product}
	secondSource := &catalogSourceStub{err: errors.New("second source must not be queried")}
	firstRepository := NewCatalogRepository(
		firstSource, firstClient, prefix, time.Minute, 5*time.Second, time.Second, nil,
	)
	secondRepository := NewCatalogRepository(
		secondSource, secondClient, prefix, time.Minute, 5*time.Second, time.Second, nil,
	)

	if _, err := firstRepository.GetByID(ctx, product.ID()); err != nil {
		t.Fatalf("first instance GetByID() error = %v", err)
	}
	got, err := secondRepository.GetByID(ctx, product.ID())
	if err != nil || got.ID() != product.ID() {
		t.Fatalf("second instance GetByID() = %+v, %v; want shared cached product", got, err)
	}
	if calls := secondSource.callCount(); calls != 0 {
		t.Fatalf("second source calls = %d, want 0", calls)
	}
}

func TestIntegrationRedisSharesRateLimitAcrossInstances(t *testing.T) {
	redisURL := strings.TrimSpace(os.Getenv("TEST_REDIS_URL"))
	if redisURL == "" {
		t.Skip("TEST_REDIS_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	firstClient, err := Open(ctx, redisURL)
	if err != nil {
		t.Fatalf("open first Redis client: %v", err)
	}
	t.Cleanup(func() { _ = firstClient.Close() })
	secondClient, err := Open(ctx, redisURL)
	if err != nil {
		t.Fatalf("open second Redis client: %v", err)
	}
	t.Cleanup(func() { _ = secondClient.Close() })
	prefix := fmt.Sprintf("integration:%d", time.Now().UnixNano())
	firstInstance := NewFixedWindowRateLimiter(firstClient, prefix, 2, time.Minute)
	secondInstance := NewFixedWindowRateLimiter(secondClient, prefix, 2, time.Minute)

	allowed, _, err := firstInstance.Allow(ctx, "same-client")
	if err != nil || !allowed {
		t.Fatalf("first instance Allow() = %v, %v; want allowed", allowed, err)
	}
	allowed, _, err = secondInstance.Allow(ctx, "same-client")
	if err != nil || !allowed {
		t.Fatalf("second instance Allow() = %v, %v; want shared second allowance", allowed, err)
	}
	allowed, retryAfter, err := firstInstance.Allow(ctx, "same-client")
	if err != nil {
		t.Fatalf("third Allow() error = %v", err)
	}
	if allowed || retryAfter <= 0 {
		t.Fatalf("third Allow() = %v, %v; want denied with retry duration", allowed, retryAfter)
	}
}
