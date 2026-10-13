package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	platformlogging "example.com/order-system/internal/platform/logging"
	"go.opentelemetry.io/otel/trace"
)

func TestLoggerFiltersLevelAndAddsService(t *testing.T) {
	var output bytes.Buffer
	logger, err := platformlogging.New(&output, platformlogging.Config{
		ServiceName: "order-system", Level: "warn",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("filtered")
	logger.Warn("retained", "component", "payment-worker")

	entry := decodeEntry(t, output.String())
	if entry["msg"] != "retained" || entry["service"] != "order-system" {
		t.Fatalf("log entry = %+v", entry)
	}
	if strings.Contains(output.String(), "filtered") {
		t.Fatalf("filtered log was emitted: %s", output.String())
	}
}

func TestLoggerAddsContextCorrelation(t *testing.T) {
	var output bytes.Buffer
	logger, err := platformlogging.New(&output, platformlogging.Config{ServiceName: "order-system"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	traceID := trace.TraceID{1, 2, 3, 4}
	spanID := trace.SpanID{5, 6, 7, 8}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))
	ctx = platformlogging.WithRequestID(ctx, "request-42")
	logger.InfoContext(ctx, "correlated event")

	entry := decodeEntry(t, output.String())
	if entry["request_id"] != "request-42" || entry["trace_id"] != traceID.String() {
		t.Fatalf("correlation fields = request_id %v trace_id %v", entry["request_id"], entry["trace_id"])
	}
}

func TestLoggerRedactsSensitiveAttributes(t *testing.T) {
	var output bytes.Buffer
	logger, err := platformlogging.New(&output, platformlogging.Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.With("api_key", "key-value").Info(
		"provider call",
		"access_token", "token-value",
		"sessionToken", "session-value",
		"APIToken", "api-token-value",
		"refreshTokenValue", "refresh-value",
		"refresh_tokens_deleted", 3,
		"request", slog.GroupValue(
			slog.String("password", "password-value"),
			slog.String("operation", "send"),
		),
	)

	entry := decodeEntry(t, output.String())
	if entry["api_key"] != platformlogging.RedactedValue || entry["access_token"] != platformlogging.RedactedValue ||
		entry["sessionToken"] != platformlogging.RedactedValue || entry["APIToken"] != platformlogging.RedactedValue ||
		entry["refreshTokenValue"] != platformlogging.RedactedValue {
		t.Fatalf("top-level sensitive fields were not redacted: %+v", entry)
	}
	if entry["refresh_tokens_deleted"] != float64(3) {
		t.Fatalf("safe aggregate was unexpectedly redacted: %+v", entry)
	}
	request, ok := entry["request"].(map[string]any)
	if !ok || request["password"] != platformlogging.RedactedValue || request["operation"] != "send" {
		t.Fatalf("nested fields = %+v", entry["request"])
	}
	for _, secret := range []string{"key-value", "token-value", "session-value", "api-token-value", "refresh-value", "password-value"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("raw secret %q appeared in log", secret)
		}
	}
}

func TestLoggerRejectsInvalidLevel(t *testing.T) {
	if _, err := platformlogging.New(&bytes.Buffer{}, platformlogging.Config{Level: "verbose"}); err == nil {
		t.Fatal("New() error = nil")
	}
}

func decodeEntry(t *testing.T, raw string) map[string]any {
	t.Helper()
	var entry map[string]any
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatalf("decode log %q: %v", raw, err)
	}
	return entry
}
