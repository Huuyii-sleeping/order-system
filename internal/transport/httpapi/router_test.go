package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/order-system/internal/transport/httpapi"
)

func TestHealthRoute(t *testing.T) {
	// httptest 可以直接调用 Handler，不需要真的监听端口。
	// 这让 HTTP 契约测试运行得更快，也不会依赖本机网络环境。
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}

	var body map[string]string
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	if got := body["status"]; got != "ok" {
		t.Errorf("response status = %q, want %q", got, "ok")
	}
}

func TestRequestIDPreservesClientValue(t *testing.T) {
	const clientRequestID = "client-request-123"

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set(httpapi.RequestIDHeader, clientRequestID)
	response := httptest.NewRecorder()

	var handlerRequestID string
	handler := httpapi.WithRequestID(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		handlerRequestID = httpapi.RequestID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	handler.ServeHTTP(response, request)

	if got := response.Header().Get(httpapi.RequestIDHeader); got != clientRequestID {
		t.Errorf("response request ID = %q, want %q", got, clientRequestID)
	}

	if handlerRequestID != clientRequestID {
		t.Errorf("handler request ID = %q, want %q", handlerRequestID, clientRequestID)
	}
}

func TestRequestIDGeneratesWhenClientValueIsMissing(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	var handlerRequestID string
	handler := httpapi.WithRequestID(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		handlerRequestID = httpapi.RequestID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	handler.ServeHTTP(response, request)

	responseRequestID := response.Header().Get(httpapi.RequestIDHeader)
	if responseRequestID == "" {
		t.Fatal("generated response request ID is empty")
	}

	if len(responseRequestID) != 32 {
		t.Errorf("generated request ID length = %d, want 32", len(responseRequestID))
	}

	if handlerRequestID != responseRequestID {
		t.Errorf(
			"handler request ID = %q, response request ID = %q",
			handlerRequestID,
			responseRequestID,
		)
	}
}

func TestHealthRouteRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	response := httptest.NewRecorder()

	httpapi.WithRequestID(httpapi.NewRouter(catalogQueriesStub{})).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf(
			"status code = %d, want %d",
			response.Code,
			http.StatusMethodNotAllowed,
		)
	}

	// ServeMux 会告诉客户端当前资源允许使用哪些 Method。
	// Go 的路由规则中，GET 同时匹配 HEAD，因此两个方法都会出现。
	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

func TestRouterReturnsNotFoundForUnknownPath(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	response := httptest.NewRecorder()

	httpapi.WithRequestID(httpapi.NewRouter(catalogQueriesStub{})).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf(
			"status code = %d, want %d",
			response.Code,
			http.StatusNotFound,
		)
	}
}
