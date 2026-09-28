package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RequestRateLimiter 是 HTTP 入口依赖的最小限流能力。
// 接口放在消费方，测试和未来的 Redis 实现都不需要依赖当前内存结构。
type RequestRateLimiter interface {
	Allow(key string) (allowed bool, retryAfter time.Duration)
}

type rateWindow struct {
	count   int
	resetAt time.Time
}

// FixedWindowRateLimiter 是单进程固定窗口限流器。
// 它适合当前模块化单体；多实例部署需要换成共享存储实现。
type FixedWindowRateLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	windows   map[string]rateWindow
	now       func() time.Time
	lastSweep time.Time
}

func NewFixedWindowRateLimiter(limit int, window time.Duration) RequestRateLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}
	return &FixedWindowRateLimiter{
		limit: limit, window: window,
		windows: make(map[string]rateWindow), now: time.Now,
	}
}

func (l *FixedWindowRateLimiter) Allow(key string) (bool, time.Duration) {
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
		return true, 0
	}
	if window.count >= l.limit {
		return false, window.resetAt.Sub(now)
	}
	window.count++
	l.windows[key] = window
	return true, 0
}

// WithRateLimit 在进入认证 Handler 前拦截过量请求。
func WithRateLimit(limiter RequestRateLimiter, next http.Handler) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := limiter.Allow(clientAddress(r))
		if !allowed {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}
