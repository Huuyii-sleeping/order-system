package httpapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	notificationapp "example.com/order-system/internal/application/notification"
	"example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/transport/httpapi"
)

type webhookUseCasesStub struct {
	event  notificationapp.WebhookEvent
	result notificationapp.WebhookResult
	err    error
}

func (s *webhookUseCasesStub) Process(_ context.Context, event notificationapp.WebhookEvent) (notificationapp.WebhookResult, error) {
	s.event = event
	return s.result, s.err
}

type webhookMetricsStub struct{ outcomes []string }

func (s *webhookMetricsStub) RecordNotificationWebhook(outcome string) {
	s.outcomes = append(s.outcomes, outcome)
}

func TestNotificationWebhookVerifiesSignatureAndPassesRawPayloadHash(t *testing.T) {
	secret := "local-test-secret"
	body := `{"event_id":"webhook-1","notification_id":"notification-1","idempotency_key":"notification-1","status":"succeeded","provider_reference":"provider-1"}`
	created, err := notification.New("notification-1", "payment-1", "order-1", "customer-1", "customer-1", "payment.succeeded.v1", 12900, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	confirmed, _, err := created.ConfirmSent("provider-1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	useCases := &webhookUseCasesStub{result: notificationapp.WebhookResult{Notification: confirmed, Processed: true}}
	metrics := &webhookMetricsStub{}
	router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, nil, nil, httpapi.RouterConfig{
		NotificationWebhooks:        useCases,
		NotificationWebhookSecurity: httpapi.NotificationWebhookSecurity{Secret: secret, Tolerance: time.Minute},
		NotificationWebhookMetrics:  metrics,
	})
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/webhooks/notifications/provider", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(httpapi.WebhookTimestampHeader, timestamp)
	request.Header.Set(httpapi.WebhookSignatureHeader, signWebhook(secret, timestamp, body))
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"processed":true`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	wantHash := sha256.Sum256([]byte(body))
	if useCases.event.ID != "webhook-1" || useCases.event.PayloadHash != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("event = %+v; want original payload hash", useCases.event)
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != "processed" {
		t.Fatalf("metrics outcomes = %v", metrics.outcomes)
	}
}

func TestNotificationWebhookRejectsInvalidOrStaleSignatureBeforeUseCase(t *testing.T) {
	for _, tt := range []struct {
		name      string
		timestamp string
		signature string
		outcome   string
	}{
		{name: "invalid signature", timestamp: strconv.FormatInt(time.Now().Unix(), 10), signature: strings.Repeat("0", 64), outcome: "invalid_signature"},
		{name: "stale timestamp", timestamp: "1", signature: strings.Repeat("0", 64), outcome: "stale"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			useCases := &webhookUseCasesStub{}
			metrics := &webhookMetricsStub{}
			router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, nil, nil, httpapi.RouterConfig{
				NotificationWebhooks:        useCases,
				NotificationWebhookSecurity: httpapi.NotificationWebhookSecurity{Secret: "local-test-secret", Tolerance: time.Minute},
				NotificationWebhookMetrics:  metrics,
			})
			request := httptest.NewRequest(http.MethodPost, "/webhooks/notifications/provider", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(httpapi.WebhookTimestampHeader, tt.timestamp)
			request.Header.Set(httpapi.WebhookSignatureHeader, tt.signature)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || useCases.event.ID != "" {
				t.Fatalf("response = %d, use case event = %+v", response.Code, useCases.event)
			}
			if len(metrics.outcomes) != 1 || metrics.outcomes[0] != tt.outcome {
				t.Fatalf("metrics outcomes = %v", metrics.outcomes)
			}
		})
	}
}

func TestNotificationWebhookMapsApplicationErrors(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantOutcome string
	}{
		{name: "invalid event", err: notificationapp.ErrInvalidWebhookEvent, wantStatus: http.StatusBadRequest, wantOutcome: "invalid_payload"},
		{name: "missing notification", err: notification.ErrNotificationNotFound, wantStatus: http.StatusNotFound, wantOutcome: "not_found"},
		{name: "conflict", err: notificationapp.ErrWebhookResultConflict, wantStatus: http.StatusConflict, wantOutcome: "conflict"},
		{name: "internal", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantOutcome: "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, timestamp := "local-test-secret", strconv.FormatInt(time.Now().Unix(), 10)
			body := `{"event_id":"webhook-1","notification_id":"notification-1","idempotency_key":"notification-1","status":"succeeded","provider_reference":"provider-1"}`
			metrics := &webhookMetricsStub{}
			router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, nil, nil, httpapi.RouterConfig{
				NotificationWebhooks:        &webhookUseCasesStub{err: tt.err},
				NotificationWebhookSecurity: httpapi.NotificationWebhookSecurity{Secret: secret, Tolerance: time.Minute},
				NotificationWebhookMetrics:  metrics,
			})
			request := httptest.NewRequest(http.MethodPost, "/webhooks/notifications/provider", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(httpapi.WebhookTimestampHeader, timestamp)
			request.Header.Set(httpapi.WebhookSignatureHeader, signWebhook(secret, timestamp, body))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != tt.wantStatus || strings.Contains(response.Body.String(), "database unavailable") {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if len(metrics.outcomes) != 1 || metrics.outcomes[0] != tt.wantOutcome {
				t.Fatalf("metrics outcomes = %v", metrics.outcomes)
			}
		})
	}
}

func TestNotificationWebhookRouteIsDisabledWithoutSecret(t *testing.T) {
	router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, nil, nil, httpapi.RouterConfig{
		NotificationWebhooks: &webhookUseCasesStub{},
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/webhooks/notifications/provider", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestNotificationWebhookRejectsOversizedBody(t *testing.T) {
	secret, timestamp := "local-test-secret", strconv.FormatInt(time.Now().Unix(), 10)
	body := strings.Repeat("x", (1<<20)+1)
	useCases := &webhookUseCasesStub{}
	metrics := &webhookMetricsStub{}
	router := httpapi.NewRouterWithConfig(catalogQueriesStub{}, nil, nil, httpapi.RouterConfig{
		NotificationWebhooks:        useCases,
		NotificationWebhookSecurity: httpapi.NotificationWebhookSecurity{Secret: secret, Tolerance: time.Minute},
		NotificationWebhookMetrics:  metrics,
	})
	request := httptest.NewRequest(http.MethodPost, "/webhooks/notifications/provider", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(httpapi.WebhookTimestampHeader, timestamp)
	request.Header.Set(httpapi.WebhookSignatureHeader, signWebhook(secret, timestamp, body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || useCases.event.ID != "" {
		t.Fatalf("response = %d, use case event = %+v", response.Code, useCases.event)
	}
	if len(metrics.outcomes) != 1 || metrics.outcomes[0] != "too_large" {
		t.Fatalf("metrics outcomes = %v, want [too_large]", metrics.outcomes)
	}
}

func signWebhook(secret, timestamp, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "." + body))
	return hex.EncodeToString(mac.Sum(nil))
}
