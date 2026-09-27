package httpapi

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// WithRecovery 捕获后续 Handler 中未处理的 panic。
//
// panic 通常表示程序出现了没有预期到的错误。Recovery 中间件会：
//
//   - 阻止 panic 继续向上传播，避免当前请求异常中断
//   - 记录请求 ID、panic 内容和调用栈
//   - 在响应尚未写出时返回统一的 JSON 500 响应
//
// Recovery 不能代替正常的 error 处理；可预期的业务失败仍然应该
// 使用 error 返回值显式处理。
func WithRecovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 单独记录 Recovery 内部看到的响应状态，
		// 用来判断 panic 发生时响应是否已经开始写出。
		recorder := &statusRecorder{ResponseWriter: w}

		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			logger.Error(
				"panic recovered",
				"request_id", RequestID(r.Context()),
				"panic", recovered,
				"stack", string(debug.Stack()),
			)

			// HTTP 状态码一旦发送就不能安全地改成 500。
			// 如果下游已经开始写响应，只记录错误并结束当前请求。
			if recorder.statusCode != 0 {
				return
			}

			_ = writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "internal server error",
			})
		}()

		next.ServeHTTP(recorder, r)
	})
}
