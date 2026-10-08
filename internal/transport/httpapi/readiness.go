package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

const defaultReadinessTimeout = 2 * time.Second

type drainState interface {
	IsDraining() bool
}

func newReadinessHandler(
	checker ReadinessChecker,
	timeout time.Duration,
	logger *slog.Logger,
) http.Handler {
	if timeout <= 0 {
		timeout = defaultReadinessTimeout
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// 排水是一次预期的生命周期转换，不是依赖故障。直接返回 503，
		// 避免继续 Ping 依赖，也避免每次健康探测都产生错误日志。
		if state, ok := checker.(drainState); ok && state.IsDraining() {
			_ = writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
			})
			return
		}
		if checker == nil {
			_ = writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		if err := checker.CheckReadiness(ctx); err != nil {
			if logger != nil {
				logger.ErrorContext(ctx, "readiness check failed", "error", err)
			}
			// 不把数据库地址或底层错误暴露给调用方；详细错误只写入服务端日志。
			_ = writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
			})
			return
		}

		_ = writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
}
