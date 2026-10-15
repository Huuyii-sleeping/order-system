package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/order-system/internal/transport/httpapi"
)

type apiLifecycleMetricsStub struct {
	accesses []string
}

func (s *apiLifecycleMetricsStub) RecordAPIAccess(version, entrypoint string) {
	s.accesses = append(s.accesses, version+":"+entrypoint)
}

func TestAPIV1AndLegacyEntrypointsShareBehaviorButExposeLifecycle(t *testing.T) {
	metrics := &apiLifecycleMetricsStub{}
	router := httpapi.NewRouterWithConfig(
		catalogQueriesStub{}, nil, nil,
		httpapi.RouterConfig{APILifecycleMetrics: metrics},
	)

	versioned := httptest.NewRecorder()
	router.ServeHTTP(versioned, httptest.NewRequest(http.MethodGet, "/api/v1/products", nil))
	if versioned.Code != http.StatusOK || versioned.Header().Get(httpapi.APIVersionHeader) != httpapi.APIVersionV1 {
		t.Fatalf("versioned response = status %d headers %v", versioned.Code, versioned.Header())
	}
	if versioned.Header().Get("Deprecation") != "" || versioned.Header().Get("Link") != "" {
		t.Fatalf("canonical v1 is marked deprecated: %v", versioned.Header())
	}

	legacy := httptest.NewRecorder()
	router.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/products", nil))
	if legacy.Code != http.StatusOK || legacy.Header().Get(httpapi.APIVersionHeader) != httpapi.APIVersionV1 {
		t.Fatalf("legacy response = status %d headers %v", legacy.Code, legacy.Header())
	}
	if !strings.HasPrefix(legacy.Header().Get("Deprecation"), "@") {
		t.Fatalf("legacy Deprecation = %q", legacy.Header().Get("Deprecation"))
	}
	if got := legacy.Header().Get("Link"); got != `</api/v1/products>; rel="successor-version"` {
		t.Fatalf("legacy successor Link = %q", got)
	}
	if got := strings.Join(metrics.accesses, ","); got != "v1:versioned,v1:legacy" {
		t.Fatalf("lifecycle metrics = %q", got)
	}
}

func TestLegacySuccessorLinkKeepsConcreteResourcePath(t *testing.T) {
	router := httpapi.NewRouter(
		catalogQueriesStub{}, authenticatedAs("customer-1"), &orderUseCasesStub{},
	)
	request := httptest.NewRequest(http.MethodGet, "/orders/order-123", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if got := response.Header().Get("Link"); got != `</api/v1/orders/order-123>; rel="successor-version"` {
		t.Fatalf("successor Link = %q", got)
	}
}
