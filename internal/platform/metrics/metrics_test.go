package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	platformmetrics "example.com/order-system/internal/platform/metrics"
)

func TestRegistryExportsHTTPAndRuntimeMetrics(t *testing.T) {
	registry := platformmetrics.NewRegistry()
	registry.RequestStarted()
	registry.RequestFinished(http.MethodGet, "/orders/{orderID}", http.StatusOK, 25*time.Millisecond)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	output := string(body)
	for _, want := range []string{
		`order_system_http_requests_total{method="GET",route="/orders/{orderID}",status="200"} 1`,
		`order_system_http_request_duration_seconds_count{method="GET",route="/orders/{orderID}"} 1`,
		`order_system_http_requests_in_flight 0`,
		`go_goroutines`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("metrics output does not contain %q", want)
		}
	}
}
