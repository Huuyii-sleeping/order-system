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
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

	next := httpapi.NewRouter(
		catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{},
	)

	// Request ID 必须在 Access Log 外层，
	// 这样 Access Log 收到的 Request 才带有请求 ID Context。
	handler := httpapi.WithRequestID(
		httpapi.WithAccessLog(logger, next),
	)

	request := httptest.NewRequest(http.MethodGet, "/orders/order-123", nil)
	request.Header.Set(httpapi.RequestIDHeader, "access-log-request")
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	logOutput := logBuffer.String()
	for _, want := range []string{
		"msg=\"http request\"",
		"request_id=access-log-request",
		"method=GET",
		"route=/orders/{orderID}",
		"status=200",
		"bytes=",
	} {
		if !strings.Contains(logOutput, want) {
			t.Errorf("log output = %q, want it to contain %q", logOutput, want)
		}
	}
	if strings.Contains(logOutput, "/orders/order-123") {
		t.Fatalf("access log contains raw resource path: %q", logOutput)
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

func TestHTTPTracingUsesRouteTemplate(t *testing.T) {
	originalProvider := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(originalProvider) })

	router := httpapi.NewRouter(
		catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{},
	)
	request := httptest.NewRequest(http.MethodGet, "/orders/order-123", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()

	httpapi.WithTracing(router).ServeHTTP(response, request)

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	if spans[0].Name() != "GET /orders/{orderID}" {
		t.Fatalf("span name = %q, want route template", spans[0].Name())
	}
}
