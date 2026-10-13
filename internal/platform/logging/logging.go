// Package logging 创建进程统一使用的结构化日志，并从 Context 补充关联字段。
package logging

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"example.com/order-system/internal/platform/telemetry"
)

const RedactedValue = "[REDACTED]"

type Config struct {
	ServiceName string
	Level       string
}

// New 创建输出 JSON 的进程级 Logger。日志级别只接受 debug、info、warn 和 error，
// 避免拼写错误时静默退回到意外的级别。
func New(writer io.Writer, config Config) (*slog.Logger, error) {
	if writer == nil {
		return nil, errors.New("log writer is nil")
	}
	level, err := parseLevel(config.Level)
	if err != nil {
		return nil, err
	}

	var handler slog.Handler = slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	handler = &contextHandler{next: handler}
	if serviceName := strings.TrimSpace(config.ServiceName); serviceName != "" {
		handler = handler.WithAttrs([]slog.Attr{slog.String("service", serviceName)})
	}
	return slog.New(handler), nil
}

// NewDefault 用于读取完整配置之前的启动日志，固定使用 info 级别。
func NewDefault(writer io.Writer, serviceName string) *slog.Logger {
	logger, err := New(writer, Config{ServiceName: serviceName, Level: "info"})
	if err != nil {
		panic(err)
	}
	return logger
}

func parseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("log level must be one of debug, info, warn, error")
	}
}

type requestIDContextKey struct{}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDContextKey{}, strings.TrimSpace(requestID))
}

func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

// contextHandler 在每条 Context 日志中自动补充 request_id 和 trace_id，
// 并对明确标记为密码、Token、Cookie、密钥等敏感属性做最后一道字段级脱敏。
// 它不能识别藏在普通错误文本中的秘密，因此调用方仍然不能记录原始请求体或凭据。
type contextHandler struct {
	next      slog.Handler
	redactAll bool
}

func (h *contextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	safeRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	hasRequestID := false
	hasTraceID := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "request_id" {
			hasRequestID = true
		}
		if attr.Key == "trace_id" {
			hasTraceID = true
		}
		safeRecord.AddAttrs(redactAttr(attr, h.redactAll))
		return true
	})
	if !hasRequestID {
		if requestID := RequestID(ctx); requestID != "" {
			safeRecord.AddAttrs(slog.String("request_id", requestID))
		}
	}
	if !hasTraceID {
		if traceID := telemetry.TraceID(ctx); traceID != "" {
			safeRecord.AddAttrs(slog.String("trace_id", traceID))
		}
	}
	return h.next.Handle(ctx, safeRecord)
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	safeAttrs := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		safeAttrs = append(safeAttrs, redactAttr(attr, h.redactAll))
	}
	return &contextHandler{next: h.next.WithAttrs(safeAttrs), redactAll: h.redactAll}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{
		next:      h.next.WithGroup(name),
		redactAll: h.redactAll || isSensitiveKey(name),
	}
}

func redactAttr(attr slog.Attr, redactAll bool) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if redactAll || isSensitiveKey(attr.Key) {
		return slog.String(attr.Key, RedactedValue)
	}
	if attr.Value.Kind() != slog.KindGroup {
		return attr
	}

	group := attr.Value.Group()
	safeGroup := make([]slog.Attr, 0, len(group))
	for _, child := range group {
		safeGroup = append(safeGroup, redactAttr(child, false))
	}
	attr.Value = slog.GroupValue(safeGroup...)
	return attr
}

func isSensitiveKey(key string) bool {
	normalized := normalizeKey(key)
	for _, safeSuffix := range []string{"_tokens_deleted", "_tokens_count", "_tokens_total"} {
		if strings.HasSuffix(normalized, safeSuffix) {
			return false
		}
	}
	for _, compound := range []string{
		"api_key", "apikey", "private_key", "privatekey", "database_url", "connection_string",
	} {
		if strings.Contains(normalized, compound) {
			return true
		}
	}
	for _, part := range strings.Split(normalized, "_") {
		switch part {
		case "password", "passwd", "secret", "authorization", "cookie", "credential", "credentials",
			"signature", "signatures", "token", "tokens", "dsn":
			return true
		}
	}
	return false
}

func normalizeKey(key string) string {
	characters := []rune(strings.TrimSpace(key))
	var normalized strings.Builder
	lastUnderscore := false
	for index, character := range characters {
		switch {
		case character >= 'A' && character <= 'Z':
			previousIsLowerOrDigit := index > 0 && ((characters[index-1] >= 'a' && characters[index-1] <= 'z') ||
				(characters[index-1] >= '0' && characters[index-1] <= '9'))
			acronymBoundary := index > 0 && index+1 < len(characters) &&
				characters[index-1] >= 'A' && characters[index-1] <= 'Z' &&
				characters[index+1] >= 'a' && characters[index+1] <= 'z'
			if normalized.Len() > 0 && !lastUnderscore && (previousIsLowerOrDigit || acronymBoundary) {
				normalized.WriteByte('_')
			}
			normalized.WriteRune(character + ('a' - 'A'))
			lastUnderscore = false
		case character == '-' || character == '.' || character == ' ' || character == '_':
			if normalized.Len() > 0 && !lastUnderscore {
				normalized.WriteByte('_')
				lastUnderscore = true
			}
		default:
			normalized.WriteRune(character)
			lastUnderscore = false
		}
	}
	return strings.Trim(normalized.String(), "_")
}
