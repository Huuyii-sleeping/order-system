package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// RequestIDHeader 是请求 ID 在 HTTP Header 中使用的名称。
//
// 将 Header 名称定义成常量，避免不同 Handler 之间出现拼写不一致。
const RequestIDHeader = "X-Request-ID"

// requestIDContextKey 是 Context 中保存请求 ID 的私有 key 类型。
//
// 不使用普通字符串作为 key，可以避免当前包和其他包使用相同字符串
// 时发生意外覆盖。
type requestIDContextKey struct{}

var fallbackRequestIDCounter uint64

// WithRequestID 为每个请求补充请求 ID。
//
// 如果客户端已经传入 X-Request-ID，就沿用它，方便跨服务追踪同一个请求。
// 如果客户端没有传入，就在入口生成一个新的 ID。
//
// 生成的 ID 会同时放到：
//
//   - 响应 Header：方便客户端和网关读取
//   - Request Context：方便后续 Handler、日志和下游调用读取
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(RequestIDHeader))
		if requestID == "" {
			requestID = generateRequestID()
		}

		// 响应 Header 必须在下游 Handler 写入响应前设置，
		// 否则 Header 可能已经被发送，客户端就看不到请求 ID。
		w.Header().Set(RequestIDHeader, requestID)

		// Request 是不可变风格使用的：不直接修改原 Request，
		// 而是创建一个带新 Context 的副本继续传递。
		ctx := context.WithValue(
			r.Context(),
			requestIDContextKey{},
			requestID,
		)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WithAccessLog 记录每个 HTTP 请求的访问日志。
//
// 访问日志属于横切能力，因此它不应该由每个业务 Handler 单独实现。
// 通过包装下一个 Handler，可以统一记录所有路由的请求信息。
func WithAccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}

		// 先让后续中间件和路由处理请求。
		// statusRecorder 会在这里记录最终状态码和响应大小。
		next.ServeHTTP(recorder, r)

		statusCode := recorder.statusCode
		if statusCode == 0 {
			// Handler 没有写入响应时，HTTP 默认状态码是 200。
			statusCode = http.StatusOK
		}

		logger.Info(
			"http request",
			"request_id", RequestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", statusCode,
			"bytes", recorder.bytesWritten,
			"duration", time.Since(startedAt),
		)
	})
}

// statusRecorder 记录下游 Handler 写入的状态码和响应大小。
//
// http.ResponseWriter 本身不会暴露“最终状态码”和“写了多少字节”，
// 所以访问日志需要用装饰器包住它。
type statusRecorder struct {
	http.ResponseWriter

	statusCode   int
	bytesWritten int
}

// WriteHeader 记录并发送响应状态码。
func (r *statusRecorder) WriteHeader(statusCode int) {
	// HTTP 响应头只能发送一次，后续重复调用不应覆盖第一次状态码。
	if r.statusCode != 0 {
		return
	}

	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Write 记录响应 body 的字节数。
func (r *statusRecorder) Write(body []byte) (int, error) {
	if r.statusCode == 0 {
		r.WriteHeader(http.StatusOK)
	}

	n, err := r.ResponseWriter.Write(body)
	r.bytesWritten += n

	return n, err
}

// Unwrap 让 http.ResponseController 能访问底层 ResponseWriter。
//
// 这为后续需要 Flush 或 Hijack 的 Handler 保留扩展能力。
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// RequestID 从 Context 中读取请求 ID。
//
// 如果当前请求没有经过 WithRequestID，返回值为空字符串。
// 返回字符串而不是暴露 Context key，可以避免调用方依赖内部实现。
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// generateRequestID 生成一个 16 字节、32 个十六进制字符的请求 ID。
//
// 请求 ID 的主要目标是可关联和低碰撞；它不是认证凭证，
// 因此这里不把它当作权限判断依据。
func generateRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}

	// 生成随机数极少失败。即使失败，请求也不应该因为日志辅助信息
	// 而无法继续处理，因此使用时间和进程内递增序号作为退化值。
	//
	// 仍然写入固定大小的字节数组，确保降级分支也返回 32 个
	// 十六进制字符，和正常分支保持相同格式。
	binary.BigEndian.PutUint64(raw[:8], uint64(time.Now().UnixNano()))
	binary.BigEndian.PutUint64(
		raw[8:],
		atomic.AddUint64(&fallbackRequestIDCounter, 1),
	)

	return hex.EncodeToString(raw[:])
}
