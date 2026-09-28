package httpapi_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	authapp "example.com/order-system/internal/application/auth"
	"example.com/order-system/internal/transport/httpapi"
)

type limiterStub struct {
	allowed bool
	calls   int
}

func (s *limiterStub) Allow(string) (bool, time.Duration) {
	s.calls++
	return s.allowed, time.Minute
}

func TestRateLimiterRejectsExcessRequestsPerClient(t *testing.T) {
	limiter := httpapi.NewFixedWindowRateLimiter(2, time.Minute)
	called := 0
	handler := httpapi.WithRateLimit(limiter, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	}))

	for requestNumber := 1; requestNumber <= 3; requestNumber++ {
		request := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		request.RemoteAddr = "192.0.2.10:42000"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		wantStatus := http.StatusNoContent
		if requestNumber == 3 {
			wantStatus = http.StatusTooManyRequests
			if response.Header().Get("Retry-After") == "" {
				t.Error("rate-limited response is missing Retry-After")
			}
		}
		if response.Code != wantStatus {
			t.Errorf("request %d status = %d, want %d", requestNumber, response.Code, wantStatus)
		}
	}
	if called != 2 {
		t.Errorf("downstream calls = %d, want 2", called)
	}
}

func TestRateLimiterDoesNotTrustForwardedForByDefault(t *testing.T) {
	limiter := httpapi.NewFixedWindowRateLimiter(1, time.Minute)
	handler := httpapi.WithRateLimit(limiter, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for index, forwardedFor := range []string{"198.51.100.1", "198.51.100.2"} {
		request := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		request.RemoteAddr = "192.0.2.10:42000"
		request.Header.Set("X-Forwarded-For", forwardedFor)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)

		want := http.StatusNoContent
		if index == 1 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Errorf("request %d status = %d, want %d", index+1, response.Code, want)
		}
	}
}

func TestLoginRouteAppliesConfiguredRateLimiterBeforeUseCase(t *testing.T) {
	auth := &authUseCasesStub{}
	limiter := &limiterStub{allowed: false}
	router := httpapi.NewRouterWithAuthSecurity(
		catalogQueriesStub{}, auth, nil,
		httpapi.AuthSecurityConfig{LoginLimiter: limiter},
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/auth/login",
		bytes.NewBufferString("{\"email\":\"learner@example.com\",\"password\":\"correct-horse\"}"),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if limiter.calls != 1 {
		t.Errorf("limiter calls = %d, want 1", limiter.calls)
	}
	if auth.loginCommand != (authapp.LoginCommand{}) {
		t.Errorf("Login() was called despite rate limit: %+v", auth.loginCommand)
	}
}
