package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/transport/httpapi"
)

type notificationQueriesStub struct {
	notification notification.Notification
	err          error
	customerID   string
	orderID      string
}

func (s *notificationQueriesStub) GetNotification(_ context.Context, customerID, orderID string) (notification.Notification, error) {
	s.customerID, s.orderID = customerID, orderID
	return s.notification, s.err
}

func TestGetNotificationByOrderReturnsSafeStatusForAuthenticatedCustomer(t *testing.T) {
	created, err := notification.New("notification-1", "payment-1", "order-1", "customer-1", "customer-1", "payment_succeeded", 12900, time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("notification.New() error = %v", err)
	}
	queries := &notificationQueriesStub{notification: created}
	router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{}, httpapi.RouterConfig{NotificationQueries: queries})
	request := httptest.NewRequest(http.MethodGet, "/orders/order-1/notification", nil)
	request.Header.Set("Authorization", "Bearer valid-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if queries.customerID != "customer-1" || queries.orderID != "order-1" {
		t.Errorf("query = customer %q, order %q", queries.customerID, queries.orderID)
	}
	body := response.Body.String()
	if !strings.Contains(body, "\"id\":\"notification-1\"") || !strings.Contains(body, "\"status\":\"pending\"") {
		t.Errorf("notification response = %s", body)
	}
	if strings.Contains(body, "provider_reference") || strings.Contains(body, "failure_reason") {
		t.Errorf("notification response exposes provider internals: %s", body)
	}
}

func TestGetNotificationByOrderRequiresAuthentication(t *testing.T) {
	queries := &notificationQueriesStub{}
	router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{}, httpapi.RouterConfig{NotificationQueries: queries})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders/order-1/notification", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if queries.orderID != "" {
		t.Fatal("unauthenticated request must not query notification")
	}
}

func TestGetNotificationByOrderMapsErrors(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{name: "not found", err: notification.ErrNotificationNotFound, wantStatus: http.StatusNotFound, wantBody: "notification not found"},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantBody: "internal server error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{}, httpapi.RouterConfig{NotificationQueries: &notificationQueriesStub{err: tt.err}})
			request := httptest.NewRequest(http.MethodGet, "/orders/order-1/notification", nil)
			request.Header.Set("Authorization", "Bearer valid-token")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf("response = %d %q, want %d containing %q", response.Code, response.Body.String(), tt.wantStatus, tt.wantBody)
			}
		})
	}
}
