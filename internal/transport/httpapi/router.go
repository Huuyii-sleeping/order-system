// Package httpapi 负责把 HTTP 请求转换为应用能够处理的输入，
// 并把处理结果转换为 HTTP 响应。
//
// 这个包属于传输层，不应该包含订单、库存等核心业务规则。
package httpapi

import (
	"encoding/json"
	"net/http"
)

// NewRouter 创建 API 使用的 HTTP 路由。
//
// 返回 http.Handler 而不是具体的 *http.ServeMux，
// 可以让调用方只依赖标准接口。以后在路由外包装中间件时，
// main.go 也不需要改变接收类型。
func NewRouter() http.Handler {
	mux := http.NewServeMux()

	// Go 1.22 之后可以在路由模式中同时声明 Method 和 Path。
	// 因此 POST /healthz 会由标准库自动返回 405 Method Not Allowed。
	mux.HandleFunc("GET /healthz", healthHandler)

	// 返回原始路由器。
	//
	// Request ID、访问日志等横切能力由应用组装层统一包装，
	// 这样中间件顺序会在一个地方清晰可见。
	return mux
}

// healthHandler 只表示当前进程是否存活并能处理 HTTP 请求。
//
// 它不检查数据库或 Redis；外部依赖的可用性属于 readiness，
// 后面接入外部依赖时会单独实现。
func healthHandler(w http.ResponseWriter, _ *http.Request) {
	// 这个响应只包含可编码的字符串，编码失败通常只可能来自
	// 客户端断开等写入问题。后面加入访问日志时再统一记录写入错误。
	_ = writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// writeJSON 统一设置 JSON 响应头、状态码并编码响应体。
//
// 将协议细节集中在辅助函数里，可以避免每个 Handler 都重复设置
// Content-Type 和调用 json.Encoder。
func writeJSON(w http.ResponseWriter, statusCode int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	return json.NewEncoder(w).Encode(value)
}
