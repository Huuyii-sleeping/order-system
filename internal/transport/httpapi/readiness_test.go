package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/transport/httpapi"
)

type readinessCheckerStub struct {
	err error
}

func (s readinessCheckerStub) CheckReadiness(context.Context) error {
	return s.err
}

func TestReadinessRouteReportsReady(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	router := httpapi.NewRouterWithConfig(
		catalogQueriesStub{}, nil, nil,
		httpapi.RouterConfig{Readiness: readinessCheckerStub{}},
	)

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"status":"ready"`) {
		t.Errorf("body = %q, want ready status", response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestReadinessRouteHidesDependencyFailure(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	response := httptest.NewRecorder()
	router := httpapi.NewRouterWithConfig(
		catalogQueriesStub{}, nil, nil,
		httpapi.RouterConfig{
			Readiness:        readinessCheckerStub{err: errors.New("database secret details")},
			ReadinessTimeout: time.Second,
		},
	)

	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(response.Body.String(), "database secret details") {
		t.Errorf("response exposed dependency error: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status":"unavailable"`) {
		t.Errorf("body = %q, want unavailable status", response.Body.String())
	}
}
