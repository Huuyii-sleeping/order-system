package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
	"example.com/order-system/internal/transport/httpapi"
)

type orderUseCasesStub struct {
	createResult  orderapp.CreateOrderResult
	createErr     error
	getResult     order.Order
	getErr        error
	listResult    orderapp.ListOrdersResult
	listErr       error
	createCommand orderapp.CreateOrderCommand
	listQuery     orderapp.ListOrdersQuery
	listCalled    bool
	getCustomerID string
	getOrderID    string
}

func (s *orderUseCasesStub) CreateOrder(_ context.Context, command orderapp.CreateOrderCommand) (orderapp.CreateOrderResult, error) {
	s.createCommand = command
	return s.createResult, s.createErr
}

func (s *orderUseCasesStub) GetOrder(_ context.Context, customerID, orderID string) (order.Order, error) {
	s.getCustomerID = customerID
	s.getOrderID = orderID
	return s.getResult, s.getErr
}

func (s *orderUseCasesStub) ListOrders(_ context.Context, query orderapp.ListOrdersQuery) (orderapp.ListOrdersResult, error) {
	s.listCalled = true
	s.listQuery = query
	return s.listResult, s.listErr
}

func TestCreateOrderReturnsCreatedOrder(t *testing.T) {
	created := mustHTTPOrder(t)
	useCases := &orderUseCasesStub{
		createResult: orderapp.CreateOrderResult{Order: created, Created: true},
	}
	router := httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases)
	request := httptest.NewRequest(
		http.MethodPost,
		"/orders",
		bytes.NewBufferString(`{"product_id":"product-1","quantity":2}`),
	)
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set(httpapi.IdempotencyKeyHeader, "key-1")
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if got := response.Header().Get("Location"); got != "/orders/order-1" {
		t.Errorf("Location = %q, want %q", got, "/orders/order-1")
	}
	if useCases.createCommand.IdempotencyKey != "key-1" || useCases.createCommand.Quantity != 2 {
		t.Errorf("CreateOrder() command = %+v", useCases.createCommand)
	}
	if useCases.createCommand.CustomerID != "customer-1" {
		t.Errorf("CreateOrder() customer ID = %q, want authenticated user", useCases.createCommand.CustomerID)
	}

	var body struct {
		ID         string `json:"id"`
		TotalCents int64  `json:"total_cents"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != "order-1" || body.TotalCents != 25800 {
		t.Errorf("response body = %+v", body)
	}
}

func TestCreateOrderIdempotentReplayReturnsOK(t *testing.T) {
	useCases := &orderUseCasesStub{
		createResult: orderapp.CreateOrderResult{Order: mustHTTPOrder(t), Created: false},
	}
	request := validCreateOrderRequest()
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Location"); got != "" {
		t.Errorf("Location = %q, want empty for replay", got)
	}
}

func TestCreateOrderRejectsInvalidHTTPInput(t *testing.T) {
	for _, tt := range []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
	}{
		{name: "missing content type", body: `{}`, wantStatus: http.StatusUnsupportedMediaType},
		{name: "malformed JSON", contentType: "application/json", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "client supplied customer ID", contentType: "application/json", body: `{"customer_id":"attacker","product_id":"product-1","quantity":1}`, wantStatus: http.StatusBadRequest},
		{name: "unknown field", contentType: "application/json", body: `{"unknown":true}`, wantStatus: http.StatusBadRequest},
		{name: "multiple JSON values", contentType: "application/json", body: `{} {}`, wantStatus: http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useCases := &orderUseCasesStub{}
			request := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(tt.body))
			if tt.contentType != "" {
				request.Header.Set("Content-Type", tt.contentType)
			}
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()

			httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
			if useCases.createCommand != (orderapp.CreateOrderCommand{}) {
				t.Fatal("invalid HTTP request must not call use case")
			}
		})
	}
}

func TestCreateOrderMapsBusinessErrors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{name: "invalid input", err: order.ErrInvalidQuantity, wantStatus: http.StatusBadRequest, wantBody: "quantity must be greater than zero"},
		{name: "missing idempotency key", err: order.ErrInvalidIdempotencyKey, wantStatus: http.StatusBadRequest, wantBody: "idempotency key is required"},
		{name: "product not found", err: catalog.ErrProductNotFound, wantStatus: http.StatusNotFound, wantBody: "product not found"},
		{name: "insufficient stock", err: inventory.ErrInsufficientStock, wantStatus: http.StatusConflict, wantBody: "insufficient stock"},
		{name: "idempotency conflict", err: orderapp.ErrIdempotencyConflict, wantStatus: http.StatusConflict, wantBody: "idempotency key conflict"},
		{name: "internal error", err: errors.New("database password leaked"), wantStatus: http.StatusInternalServerError, wantBody: "internal server error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useCases := &orderUseCasesStub{createErr: tt.err}
			response := httptest.NewRecorder()

			httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, validCreateOrderRequest())

			if response.Code != tt.wantStatus {
				t.Fatalf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
			if response.Body.String() != `{"error":"`+tt.wantBody+`"}`+"\n" {
				t.Errorf("response body = %q", response.Body.String())
			}
		})
	}
}

func TestGetOrder(t *testing.T) {
	useCases := &orderUseCasesStub{getResult: mustHTTPOrder(t)}
	request := httptest.NewRequest(http.MethodGet, "/orders/order-1", nil)
	response := httptest.NewRecorder()

	request.Header.Set("Authorization", "Bearer valid-token")
	httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if useCases.getOrderID != "order-1" {
		t.Errorf("GetOrder() ID = %q, want order-1", useCases.getOrderID)
	}
	if useCases.getCustomerID != "customer-1" {
		t.Errorf("GetOrder() customer ID = %q, want customer-1", useCases.getCustomerID)
	}
}

func TestGetOrderReturnsNotFound(t *testing.T) {
	useCases := &orderUseCasesStub{getErr: order.ErrOrderNotFound}
	request := httptest.NewRequest(http.MethodGet, "/orders/missing", nil)
	response := httptest.NewRecorder()

	request.Header.Set("Authorization", "Bearer valid-token")
	httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestListOrdersUsesAuthenticatedCustomerAndReturnsCursor(t *testing.T) {
	useCases := &orderUseCasesStub{listResult: orderapp.ListOrdersResult{
		Orders: []order.Order{
			mustHTTPOrderWith(t, "order-2", time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)),
			mustHTTPOrderWith(t, "order-1", time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)),
		},
		NextCursor: "next-page-token",
	}}
	request := httptest.NewRequest(http.MethodGet, "/orders?limit=2&cursor=current-page-token", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if useCases.listQuery.CustomerID != "customer-1" || useCases.listQuery.Limit != 2 ||
		useCases.listQuery.Cursor != "current-page-token" {
		t.Errorf("ListOrders() query = %+v", useCases.listQuery)
	}
	var body struct {
		Items      []orderResponseFixture `json:"items"`
		NextCursor string                 `json:"next_cursor"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 2 || body.Items[0].ID != "order-2" || body.NextCursor != "next-page-token" {
		t.Errorf("response body = %+v", body)
	}
}

func TestListOrdersRejectsInvalidLimitBeforeCallingUseCase(t *testing.T) {
	for _, query := range []string{
		"?limit=0", "?limit=101", "?limit=abc", "?limit=1&limit=2", "?customer_id=attacker",
	} {
		t.Run(query, func(t *testing.T) {
			useCases := &orderUseCasesStub{}
			request := httptest.NewRequest(http.MethodGet, "/orders"+query, nil)
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()

			httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status code = %d, want %d", response.Code, http.StatusBadRequest)
			}
			if useCases.listCalled {
				t.Fatal("invalid HTTP query must not call use case")
			}
		})
	}
}

func TestListOrdersMapsApplicationErrors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid cursor", err: orderapp.ErrInvalidListCursor, wantStatus: http.StatusBadRequest},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useCases := &orderUseCasesStub{listErr: tt.err}
			request := httptest.NewRequest(http.MethodGet, "/orders?cursor=opaque", nil)
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()

			httpapi.NewRouter(catalogQueriesStub{}, authenticatedAs("customer-1"), useCases).ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

type orderResponseFixture struct {
	ID string `json:"id"`
}

func validCreateOrderRequest() *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/orders",
		bytes.NewBufferString(`{"product_id":"product-1","quantity":2}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(httpapi.IdempotencyKeyHeader, "key-1")
	request.Header.Set("Authorization", "Bearer valid-token")
	return request
}

func mustHTTPOrder(t *testing.T) order.Order {
	return mustHTTPOrderWith(t, "order-1", time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC))
}

func mustHTTPOrderWith(t *testing.T, orderID string, createdAt time.Time) order.Order {
	t.Helper()
	created, err := order.New(
		orderID,
		"customer-1",
		"product-1",
		2,
		12900,
		"key-1",
		createdAt,
	)
	if err != nil {
		t.Fatalf("create order fixture: %v", err)
	}
	return created
}
