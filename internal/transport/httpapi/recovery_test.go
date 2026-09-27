package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/order-system/internal/transport/httpapi"
)

func TestRecoveryReturnsInternalServerError(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	panicHandler := http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		panic("unexpected failure")
	})

	// 使用和生产代码相同的顺序，确保 Recovery 返回的 500
	// 会被 Access Log 记录，请求 ID 也能进入两类日志。
	handler := httpapi.WithRequestID(
		httpapi.WithAccessLog(
			logger,
			httpapi.WithRecovery(logger, panicHandler),
		),
	)

	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set(httpapi.RequestIDHeader, "panic-request-42")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf(
			"status code = %d, want %d",
			response.Code,
			http.StatusInternalServerError,
		)
	}

	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	if got := body["error"]; got != "internal server error" {
		t.Errorf("error response = %q, want %q", got, "internal server error")
	}

	logOutput := logBuffer.String()
	for _, want := range []string{
		"msg=\"panic recovered\"",
		"request_id=panic-request-42",
		"panic=\"unexpected failure\"",
		"status=500",
	} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("log output does not contain %q", want)
		}
	}
}

func TestRecoveryDoesNotOverwriteStartedResponse(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	panicHandler := http.HandlerFunc(func(
		w http.ResponseWriter,
		_ *http.Request,
	) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("partial response"))
		panic("failure after response started")
	})

	handler := httpapi.WithRecovery(logger, panicHandler)
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusAccepted)
	}

	if got := response.Body.String(); got != "partial response" {
		t.Errorf("response body = %q, want %q", got, "partial response")
	}

	if !strings.Contains(logBuffer.String(), "panic recovered") {
		t.Error("panic was not recorded")
	}
}

func TestRecoveryPassesThroughNormalResponse(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	httpapi.WithRecovery(logger, next).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/normal", nil),
	)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNoContent)
	}

	if logBuffer.Len() != 0 {
		t.Errorf("normal request produced recovery log: %q", logBuffer.String())
	}
}
