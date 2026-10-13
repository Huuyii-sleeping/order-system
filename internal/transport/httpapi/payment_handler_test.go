package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/order-system/internal/domain/payment"
	"example.com/order-system/internal/transport/httpapi"
)

type paymentQueriesStub struct {
	payment    payment.Payment
	err        error
	customerID string
	orderID    string
}

func (s *paymentQueriesStub) GetPayment(
	_ context.Context, customerID, orderID string,
) (payment.Payment, error) {
	s.customerID = customerID
	s.orderID = orderID
	return s.payment, s.err
}

func TestGetPaymentByOrderReturnsAuthenticatedCustomersPayment(t *testing.T) {
	created, err := payment.New(
		"payment-1", "order-1", "customer-1", 25800,
		time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("payment.New() error = %v", err)
	}
	queries := &paymentQueriesStub{payment: created}
	router := httpapi.NewRouterWithConfig(
		catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{},
		httpapi.RouterConfig{PaymentQueries: queries},
	)
	request := httptest.NewRequest(http.MethodGet, "/orders/order-1/payment", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if queries.customerID != "customer-1" || queries.orderID != "order-1" {
		t.Errorf("query = customer %q, order %q", queries.customerID, queries.orderID)
	}
	var body struct {
		ID                string         `json:"id"`
		OrderID           string         `json:"order_id"`
		AmountCents       int64          `json:"amount_cents"`
		Status            payment.Status `json:"status"`
		ProviderReference *string        `json:"provider_reference"`
		FailureReason     *string        `json:"failure_reason"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode payment response: %v", err)
	}
	if body.ID != "payment-1" || body.OrderID != "order-1" ||
		body.AmountCents != 25800 || body.Status != payment.StatusPending {
		t.Errorf("payment response = %+v", body)
	}
	if body.ProviderReference != nil || body.FailureReason != nil {
		t.Errorf("payment response exposes internal provider details: %+v", body)
	}
}

func TestGetPaymentByOrderRequiresAuthentication(t *testing.T) {
	queries := &paymentQueriesStub{}
	router := httpapi.NewRouterWithConfig(
		catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{},
		httpapi.RouterConfig{PaymentQueries: queries},
	)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders/order-1/payment", nil))

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if queries.orderID != "" {
		t.Fatal("unauthenticated request must not query payment")
	}
}

func TestGetPaymentByOrderMapsNotFoundAndInternalErrors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantBody   string
	}{
		{name: "not found", err: payment.ErrPaymentNotFound, wantStatus: http.StatusNotFound, wantCode: "payment_not_found", wantBody: "payment not found"},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error", wantBody: "internal server error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			router := httpapi.NewRouterWithConfig(
				catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{},
				httpapi.RouterConfig{PaymentQueries: &paymentQueriesStub{err: tt.err}},
			)
			request := httptest.NewRequest(http.MethodGet, "/orders/order-1/payment", nil)
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
			want := `{"code":"` + tt.wantCode + `","error":"` + tt.wantBody + `"}` + "\n"
			if response.Body.String() != want {
				t.Errorf("response body = %q, want %q", response.Body.String(), want)
			}
		})
	}
}
