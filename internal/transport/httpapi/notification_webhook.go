package httpapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	notificationapp "example.com/order-system/internal/application/notification"
	domainnotification "example.com/order-system/internal/domain/notification"
)

const (
	WebhookTimestampHeader = "X-Webhook-Timestamp"
	WebhookSignatureHeader = "X-Webhook-Signature"
)

var (
	errInvalidWebhookSignature = errors.New("invalid webhook signature")
	errStaleWebhookTimestamp   = errors.New("webhook timestamp outside tolerance")
)

type NotificationWebhookUseCases interface {
	Process(context.Context, notificationapp.WebhookEvent) (notificationapp.WebhookResult, error)
}

type NotificationWebhookMetrics interface {
	RecordNotificationWebhook(string)
}

type NotificationWebhookSecurity struct {
	Secret    string
	Tolerance time.Duration
}

type notificationWebhookHandler struct {
	useCases  NotificationWebhookUseCases
	secret    []byte
	tolerance time.Duration
	now       func() time.Time
	metrics   NotificationWebhookMetrics
}

type notificationWebhookRequest struct {
	EventID           string                     `json:"event_id"`
	NotificationID    string                     `json:"notification_id"`
	IdempotencyKey    string                     `json:"idempotency_key"`
	Status            notificationapp.SendStatus `json:"status"`
	ProviderReference string                     `json:"provider_reference,omitempty"`
	Reason            string                     `json:"reason,omitempty"`
}

func newNotificationWebhookHandler(useCases NotificationWebhookUseCases, security NotificationWebhookSecurity, metrics NotificationWebhookMetrics) *notificationWebhookHandler {
	tolerance := security.Tolerance
	if tolerance <= 0 {
		tolerance = 5 * time.Minute
	}
	return &notificationWebhookHandler{
		useCases: useCases, secret: []byte(security.Secret), tolerance: tolerance,
		now: time.Now, metrics: metrics,
	}
}

func (h *notificationWebhookHandler) receive(w http.ResponseWriter, r *http.Request) {
	if !hasJSONContentType(r.Header.Get("Content-Type")) {
		h.record("invalid_payload")
		writeError(w, http.StatusUnsupportedMediaType, errorCodeContentTypeRequired, "content type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.record("invalid_payload")
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
		return
	}
	if err := h.verify(r.Header.Get(WebhookTimestampHeader), r.Header.Get(WebhookSignatureHeader), body); err != nil {
		if errors.Is(err, errStaleWebhookTimestamp) {
			h.record("stale")
		} else {
			h.record("invalid_signature")
		}
		writeError(w, http.StatusUnauthorized, errorCodeInvalidWebhookSignature, "invalid webhook signature")
		return
	}

	var request notificationWebhookRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.record("invalid_payload")
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.record("invalid_payload")
		writeError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
		return
	}

	payloadHash := fmt.Sprintf("%x", sha256.Sum256(body))
	result, err := h.useCases.Process(r.Context(), notificationapp.WebhookEvent{
		ID: request.EventID, NotificationID: request.NotificationID,
		IdempotencyKey: request.IdempotencyKey, Status: request.Status,
		ProviderReference: request.ProviderReference, Reason: request.Reason,
		PayloadHash: payloadHash,
	})
	if err != nil {
		h.writeProcessError(w, err)
		return
	}
	outcome := "duplicate"
	if result.Processed {
		outcome = "processed"
	}
	h.record(outcome)
	_ = writeJSON(w, http.StatusOK, map[string]any{
		"status": result.Notification.Status(), "processed": result.Processed,
	})
}

func (h *notificationWebhookHandler) verify(timestampHeader, signatureHeader string, body []byte) error {
	if len(h.secret) == 0 {
		return errInvalidWebhookSignature
	}
	timestamp, err := strconv.ParseInt(strings.TrimSpace(timestampHeader), 10, 64)
	if err != nil {
		return errInvalidWebhookSignature
	}
	receivedAt := time.Unix(timestamp, 0)
	delta := h.now().Sub(receivedAt)
	if delta < 0 {
		delta = -delta
	}
	if delta > h.tolerance {
		return errStaleWebhookTimestamp
	}
	provided, err := hex.DecodeString(strings.TrimSpace(signatureHeader))
	if err != nil || len(provided) != sha256.Size {
		return errInvalidWebhookSignature
	}
	mac := hmac.New(sha256.New, h.secret)
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return errInvalidWebhookSignature
	}
	return nil
}

func (h *notificationWebhookHandler) writeProcessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notificationapp.ErrInvalidWebhookEvent):
		h.record("invalid_payload")
		writeError(w, http.StatusBadRequest, errorCodeInvalidWebhook, "invalid webhook event")
	case errors.Is(err, domainnotification.ErrNotificationNotFound):
		h.record("not_found")
		writeError(w, http.StatusNotFound, errorCodeNotificationNotFound, "notification not found")
	case errors.Is(err, notificationapp.ErrWebhookEventConflict), errors.Is(err, notificationapp.ErrWebhookResultConflict):
		h.record("conflict")
		writeError(w, http.StatusConflict, errorCodeWebhookConflict, "webhook event conflicts with existing result")
	default:
		h.record("error")
		writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
	}
}

func (h *notificationWebhookHandler) record(outcome string) {
	if h.metrics != nil {
		h.metrics.RecordNotificationWebhook(outcome)
	}
}
