package diagnostics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlerExposesProfilesWithoutBusinessRoutes(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		path             string
		wantStatus       int
		wantBodyContains string
	}{
		{
			name: "index lists runtime profiles", method: http.MethodGet,
			path: "/debug/pprof/", wantStatus: http.StatusOK, wantBodyContains: "goroutine",
		},
		{
			name: "goroutine profile is available", method: http.MethodGet,
			path: "/debug/pprof/goroutine?debug=1", wantStatus: http.StatusOK,
			wantBodyContains: "goroutine profile",
		},
		{
			name: "business health route is absent", method: http.MethodGet,
			path: "/healthz", wantStatus: http.StatusNotFound,
		},
		{
			name: "profile endpoints only accept get", method: http.MethodPost,
			path: "/debug/pprof/", wantStatus: http.StatusMethodNotAllowed,
		},
	}

	handler := NewHandler()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if test.wantBodyContains != "" && !strings.Contains(response.Body.String(), test.wantBodyContains) {
				t.Fatalf("body does not contain %q", test.wantBodyContains)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
			if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}

func TestNewRejectsNonLoopbackAddresses(t *testing.T) {
	for _, address := range []string{
		":6060",
		"0.0.0.0:6060",
		"[::]:6060",
		"192.0.2.10:6060",
		"localhost:6060",
	} {
		t.Run(address, func(t *testing.T) {
			server, err := New(Config{Address: address})
			if err == nil {
				_ = server.Close()
				t.Fatalf("New() error = nil, want loopback validation error")
			}
		})
	}
}

func TestServerServesAndDrains(t *testing.T) {
	server, err := New(Config{Address: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	stopCtx, stop := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWork()
	done := make(chan error, 1)
	go func() {
		done <- server.RunGracefully(stopCtx, workCtx)
	}()

	client := &http.Client{Timeout: 100 * time.Millisecond}
	url := "http://" + server.Address() + "/debug/pprof/"
	deadline := time.After(time.Second)
	for {
		response, requestErr := client.Get(url)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.StatusCode)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("diagnostics server did not become ready: %v", requestErr)
		case <-time.After(10 * time.Millisecond):
		}
	}

	stop()
	select {
	case runErr := <-done:
		if runErr != nil {
			t.Fatalf("RunGracefully() error = %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("RunGracefully() did not stop")
	}
}

func TestNewFailsBeforeServingWhenAddressIsAlreadyInUse(t *testing.T) {
	first, err := New(Config{Address: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := New(Config{Address: first.Address()})
	if err == nil {
		_ = second.Close()
		t.Fatal("second New() error = nil, want address-in-use error")
	}
}

func TestNewRejectsNegativeSamplingRates(t *testing.T) {
	tests := []Config{
		{Address: "127.0.0.1:0", MutexProfileFraction: -1},
		{Address: "127.0.0.1:0", BlockProfileRate: -1},
	}
	for _, config := range tests {
		if server, err := New(config); err == nil {
			_ = server.Close()
			t.Fatalf("New(%+v) error = nil, want validation error", config)
		}
	}
}
