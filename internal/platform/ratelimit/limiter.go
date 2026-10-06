// Package ratelimit 定义跨传输层和基础设施适配器共享的限流契约。
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter 判断某个稳定键在当前窗口内是否仍可继续请求。
//
// error 与“不允许”必须分开：前者表示限流基础设施不可用，后者表示调用方
// 已经用完配额。HTTP 层会分别映射为 503 和 429。
type Limiter interface {
	Allow(context.Context, string) (allowed bool, retryAfter time.Duration, err error)
}

type rateWindow struct {
	count   int
	resetAt time.Time
}

// FixedWindow 是单进程固定窗口限流器。它适合无需跨实例共享额度的
// 本地模式；启用 Redis 后，组合根会把它替换成共享实现。
type FixedWindow struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	windows   map[string]rateWindow
	now       func() time.Time
	lastSweep time.Time
}

func NewFixedWindow(limit int, window time.Duration) Limiter {
	if limit <= 0 || window <= 0 {
		return nil
	}
	return &FixedWindow{
		limit: limit, window: window, windows: make(map[string]rateWindow), now: time.Now,
	}
}

func (l *FixedWindow) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return false, 0, err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if l.lastSweep.IsZero() || !l.lastSweep.Add(l.window).After(now) {
		for candidate, window := range l.windows {
			if !window.resetAt.After(now) {
				delete(l.windows, candidate)
			}
		}
		l.lastSweep = now
	}
	window, exists := l.windows[key]
	if !exists || !window.resetAt.After(now) {
		l.windows[key] = rateWindow{count: 1, resetAt: now.Add(l.window)}
		return true, 0, nil
	}
	if window.count >= l.limit {
		return false, window.resetAt.Sub(now), nil
	}
	window.count++
	l.windows[key] = window
	return true, 0, nil
}
