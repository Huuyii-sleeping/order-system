package httpapi

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"example.com/order-system/internal/platform/ratelimit"
)

// RequestRateLimiter 是 HTTP 入口依赖的最小限流能力。这里导出共享契约的
// 别名，让路由配置仍然使用传输层语言，而具体实现可以位于其他包。
type RequestRateLimiter = ratelimit.Limiter

func NewFixedWindowRateLimiter(limit int, window time.Duration) RequestRateLimiter {
	return ratelimit.NewFixedWindow(limit, window)
}

// WithRateLimit 在进入认证 Handler 前拦截过量请求。
func WithRateLimit(limiter RequestRateLimiter, next http.Handler) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter, err := limiter.Allow(r.Context(), clientAddress(r))
		if err != nil {
			// 认证入口的共享限流器不可用时选择 fail closed。否则 Redis
			// 故障会悄悄移除暴力破解保护。响应不暴露底层连接信息。
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, errorCodeServiceUnavailable, "service temporarily unavailable")
			return
		}
		if !allowed {
			seconds := int(math.Ceil(retryAfter.Seconds()))
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeError(w, http.StatusTooManyRequests, errorCodeRateLimited, "too many requests")
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
