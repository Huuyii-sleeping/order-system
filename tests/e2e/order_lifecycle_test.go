package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestOrderLifecycleAcrossRealInfrastructure 从公开 HTTP API 验证完整链路：
// 注册登录 -> PostgreSQL 事务下单 -> Outbox -> Kafka -> 支付 -> 订单确认
// -> 通知投递。测试只观察公共接口，不绕过传输层读取数据库内部状态。
func TestOrderLifecycleAcrossRealInfrastructure(t *testing.T) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("TEST_API_URL")), "/")
	if baseURL == "" {
		t.Skip("set TEST_API_URL to run end-to-end tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	apiURL := baseURL + "/api/v1"

	assertEndpointStatus(t, ctx, client, baseURL+"/healthz", http.StatusOK)
	assertEndpointStatus(t, ctx, client, baseURL+"/readyz", http.StatusOK)

	testID := time.Now().UTC().UnixNano()
	email := fmt.Sprintf("learner-%d@example.test", testID)
	password := "integration-password"

	status, _ := requestJSON(t, ctx, client, http.MethodPost, apiURL+"/auth/register", map[string]any{
		"email": email, "password": password,
	}, nil, nil)
	if status != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", status, http.StatusCreated)
	}

	var login struct {
		AccessToken string `json:"access_token"`
	}
	status, _ = requestJSON(t, ctx, client, http.MethodPost, apiURL+"/auth/login", map[string]any{
		"email": email, "password": password,
	}, nil, &login)
	if status != http.StatusOK || login.AccessToken == "" {
		t.Fatalf("login status/token = %d/%t, want 200/non-empty", status, login.AccessToken != "")
	}

	authHeaders := map[string]string{"Authorization": "Bearer " + login.AccessToken}
	createHeaders := map[string]string{
		"Authorization":   "Bearer " + login.AccessToken,
		"Idempotency-Key": fmt.Sprintf("e2e-order-%d", testID),
	}
	orderRequest := map[string]any{"product_id": "product-1", "quantity": 1}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	status, _ = requestJSON(
		t, ctx, client, http.MethodPost, apiURL+"/orders", orderRequest, createHeaders, &created,
	)
	if status != http.StatusCreated || created.ID == "" || created.Status != "pending" {
		t.Fatalf("create order = status %d, body %+v; want 201 pending order", status, created)
	}

	// 客户端因响应丢失而重试时，应返回同一个订单而不是再次扣减库存。
	var retried struct {
		ID string `json:"id"`
	}
	status, _ = requestJSON(
		t, ctx, client, http.MethodPost, apiURL+"/orders", orderRequest, createHeaders, &retried,
	)
	if status != http.StatusOK || retried.ID != created.ID {
		t.Fatalf("idempotent retry = status %d, order %q; want 200 and %q", status, retried.ID, created.ID)
	}

	orderURL := apiURL + "/orders/" + created.ID
	waitForResourceStatus(t, ctx, client, orderURL, authHeaders, "confirmed")
	waitForResourceStatus(t, ctx, client, orderURL+"/payment", authHeaders, "succeeded")
	waitForResourceStatus(t, ctx, client, orderURL+"/notification", authHeaders, "sent")

	var orders struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	status, _ = requestJSON(t, ctx, client, http.MethodGet, apiURL+"/orders?limit=10", nil, authHeaders, &orders)
	if status != http.StatusOK || !containsOrder(orders.Items, created.ID) {
		t.Fatalf("list orders = status %d, items %+v; want created order %q", status, orders.Items, created.ID)
	}
}

func waitForResourceStatus(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	url string,
	headers map[string]string,
	want string,
) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	lastHTTPStatus := 0
	lastResourceStatus := ""
	for {
		var resource struct {
			Status string `json:"status"`
		}
		status, _ := requestJSON(t, ctx, client, http.MethodGet, url, nil, headers, &resource)
		lastHTTPStatus, lastResourceStatus = status, resource.Status
		if status == http.StatusOK && resource.Status == want {
			return
		}
		if status != http.StatusOK && status != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 200 or transient 404", url, status)
		}

		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for %s status %q: %v; last HTTP/resource status = %d/%q",
				url, want, ctx.Err(), lastHTTPStatus, lastResourceStatus,
			)
		case <-ticker.C:
		}
	}
}

func requestJSON(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	method string,
	url string,
	body any,
	headers map[string]string,
	destination any,
) (int, []byte) {
	t.Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, requestBody)
	if err != nil {
		t.Fatalf("create %s %s request: %v", method, url, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("execute %s %s: %v", method, url, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		t.Fatalf("read %s %s response: %v", method, url, err)
	}
	if destination != nil && len(payload) > 0 && response.StatusCode < http.StatusInternalServerError {
		if err := json.Unmarshal(payload, destination); err != nil {
			t.Fatalf("decode %s %s response %q: %v", method, url, payload, err)
		}
	}
	return response.StatusCode, payload
}

func assertEndpointStatus(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	url string,
	want int,
) {
	t.Helper()
	status, _ := requestJSON(t, ctx, client, http.MethodGet, url, nil, nil, nil)
	if status != want {
		t.Fatalf("GET %s status = %d, want %d", url, status, want)
	}
}

func containsOrder(items []struct {
	ID string `json:"id"`
}, orderID string) bool {
	for _, item := range items {
		if item.ID == orderID {
			return true
		}
	}
	return false
}
