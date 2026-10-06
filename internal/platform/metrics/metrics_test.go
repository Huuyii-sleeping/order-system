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
	registry.RecordProcessingAttempt("payments-v1", "orders.created.v1", "success")
	registry.RecordProcessingAttempt("payments-v1", "orders.created.v1", "failure")
	registry.RecordRetry("payments-v1", "orders.created.v1", "handler")
	registry.RecordDeadLetter("payments-v1", "orders.created.v1")
	registry.RecordOffsetCommit("payments-v1", "orders.created.v1", "success")
	registry.SetConsumerLag("payments-v1", "orders.created.v1", 2, 7)
	registry.RecordCatalogCacheOperation("hit")

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
		`order_system_kafka_processing_attempts_total{consumer_group="payments-v1",outcome="success",topic="orders.created.v1"} 1`,
		`order_system_kafka_processing_attempts_total{consumer_group="payments-v1",outcome="failure",topic="orders.created.v1"} 1`,
		`order_system_kafka_retries_total{consumer_group="payments-v1",reason="handler",topic="orders.created.v1"} 1`,
		`order_system_kafka_dead_letters_published_total{consumer_group="payments-v1",topic="orders.created.v1"} 1`,
		`order_system_kafka_offset_commits_total{consumer_group="payments-v1",outcome="success",topic="orders.created.v1"} 1`,
		`order_system_kafka_consumer_lag_records{consumer_group="payments-v1",partition="2",topic="orders.created.v1"} 7`,
		`order_system_catalog_cache_operations_total{outcome="hit"} 1`,
		`go_goroutines`,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("metrics output does not contain %q", want)
		}
	}
}

func TestRegistryDeletesLagForRevokedPartition(t *testing.T) {
	registry := platformmetrics.NewRegistry()
	registry.SetConsumerLag("payments-v1", "orders.created.v1", 2, 7)
	registry.DeleteConsumerLag("payments-v1", "orders.created.v1", 2)

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatalf("read metrics response: %v", err)
	}
	if strings.Contains(string(body), `order_system_kafka_consumer_lag_records{consumer_group="payments-v1"`) {
		t.Fatal("revoked partition lag remains exported")
	}
}
