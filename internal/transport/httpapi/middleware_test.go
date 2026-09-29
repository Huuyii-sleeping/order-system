package httpapi_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/transport/httpapi"
)

type HTTPMetricsStub struct {
	started    int
	method     string
	route      string
	statusCode int
	duration   time.Duration
}

func (s *HTTPMetricsStub) RequestStarted() {
	s.started++
}

func (s *HTTPMetricsStub) RequestFinished(
	method string, route string, statusCode int, duration time.Duration,
) {
	s.method = method
	s.route = route
	s.statusCode = statusCode
	s.duration = duration
}

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

func TestHTTPMetricsUsesMatchedRoutePattern(t *testing.T) {
	metrics := &HTTPMetricsStub{}
	router := httpapi.NewRouter(
		catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{},
	)
	request := httptest.NewRequest(http.MethodGet, "/orders/order-123", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	httpapi.WithHTTPMetrics(metrics, router).ServeHTTP(response, request)

	if metrics.started != 1 {
		t.Errorf("started requests = %d, want 1", metrics.started)
	}
	if metrics.method != http.MethodGet || metrics.route != "/orders/{orderID}" {
		t.Errorf("metric labels = %q %q, want GET /orders/{orderID}", metrics.method, metrics.route)
	}
	if metrics.statusCode != http.StatusOK || metrics.duration <= 0 {
		t.Errorf("metric result = status %d, duration %s", metrics.statusCode, metrics.duration)
	}
}

func TestHTTPMetricsBoundsUnknownRoutes(t *testing.T) {
	metrics := &HTTPMetricsStub{}
	request := httptest.NewRequest(http.MethodGet, "/unknown/customer-provided-value", nil)
	response := httptest.NewRecorder()

	httpapi.WithHTTPMetrics(
		metrics, httpapi.NewRouter(catalogQueriesStub{}, nil, nil),
	).ServeHTTP(response, request)

	if metrics.route != "unmatched" || metrics.statusCode != http.StatusNotFound {
		t.Errorf("metric result = route %q status %d", metrics.route, metrics.statusCode)
	}
}
