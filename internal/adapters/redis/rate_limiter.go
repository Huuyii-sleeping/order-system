package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

type fixedWindowCounter interface {
	IncrementWindow(context.Context, string, time.Duration) (int64, time.Duration, error)
}

// FixedWindowRateLimiter 把计数放在共享 Redis 中，所以不同应用实例看到的是
// 同一个额度。客户端地址先哈希，避免把原始地址直接暴露在 Redis key 中。
type FixedWindowRateLimiter struct {
	counter   fixedWindowCounter
	keyPrefix string
	limit     int64
	window    time.Duration
}

func NewFixedWindowRateLimiter(
	counter fixedWindowCounter, keyPrefix string, limit int, window time.Duration,
) *FixedWindowRateLimiter {
	return &FixedWindowRateLimiter{
		counter: counter, keyPrefix: keyPrefix, limit: int64(limit), window: window,
	}
}

func (l *FixedWindowRateLimiter) Allow(
	ctx context.Context, key string,
) (bool, time.Duration, error) {
	digest := sha256.Sum256([]byte(key))
	redisKey := l.keyPrefix + ":" + hex.EncodeToString(digest[:])
	count, remaining, err := l.counter.IncrementWindow(ctx, redisKey, l.window)
	if err != nil {
		return false, 0, err
	}
	if count <= l.limit {
		return true, 0, nil
	}
	if remaining < time.Millisecond {
		remaining = time.Millisecond
	}
	return false, remaining, nil
}
