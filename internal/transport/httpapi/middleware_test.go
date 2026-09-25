package httpapi_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/order-system/internal/transport/httpapi"
)

func TestAccessLogRecordsRequestDetails(t *testing.T) {
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, nil))

	// 用一个简单的下游 Handler 模拟业务接口。
	// 它返回 201 和 7 个字节，方便验证状态码与响应大小。
	next := http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})

	// Request ID 必须在 Access Log 外层，
	// 这样 Access Log 收到的 Request 才带有请求 ID Context。
	handler := httpapi.WithRequestID(
		httpapi.WithAccessLog(logger, next),
	)

	request := httptest.NewRequest(http.MethodGet, "/orders", nil)
	request.Header.Set(httpapi.RequestIDHeader, "access-log-request")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusCreated)
	}

	logOutput := logBuffer.String()
	for _, want := range []string{
		"msg=\"http request\"",
		"request_id=access-log-request",
		"method=GET",
		"path=/orders",
		"status=201",
		"bytes=7",
	} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("log output = %q, want it to contain %q", logOutput, want)
		}
	}
}
