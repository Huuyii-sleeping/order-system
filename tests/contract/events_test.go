package contract_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"example.com/order-system/internal/application/event"
)

func TestVersionedEventTopics(t *testing.T) {
	want := map[string]string{
		"order_created":          "orders.created.v1",
		"payment_succeeded":      "payments.succeeded.v1",
		"payment_failed":         "payments.failed.v1",
		"notification_requested": "notifications.requested.v1",
	}
	got := map[string]string{
		"order_created":          event.OrderCreatedV1Topic,
		"payment_succeeded":      event.PaymentSucceededV1Topic,
		"payment_failed":         event.PaymentFailedV1Topic,
		"notification_requested": event.NotificationRequestedV1Topic,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("event topics changed: got %#v, want %#v", got, want)
	}
}

func TestOrderCreatedV1JSONContract(t *testing.T) {
	assertJSONFields(t, event.OrderCreatedV1{
		OrderID:    "order-1",
		CustomerID: "customer-1",
		ProductID:  "product-1",
		Quantity:   2,
		TotalCents: 12900,
		OccurredAt: contractTime(),
	}, map[string]string{
		"order_id":    `"order-1"`,
		"customer_id": `"customer-1"`,
		"product_id":  `"product-1"`,
		"quantity":    `2`,
		"total_cents": `12900`,
		"occurred_at": `"2026-01-02T03:04:05Z"`,
	})
}

func TestPaymentResultV1JSONContracts(t *testing.T) {
	fields := map[string]string{
		"payment_id":   `"payment-1"`,
		"order_id":     `"order-1"`,
		"customer_id":  `"customer-1"`,
		"amount_cents": `12900`,
		"occurred_at":  `"2026-01-02T03:04:05Z"`,
	}

	t.Run("succeeded", func(t *testing.T) {
		assertJSONFields(t, event.PaymentSucceededV1{
			PaymentID: "payment-1", OrderID: "order-1", CustomerID: "customer-1",
			AmountCents: 12900, OccurredAt: contractTime(),
		}, fields)
	})
	t.Run("failed", func(t *testing.T) {
		assertJSONFields(t, event.PaymentFailedV1{
			PaymentID: "payment-1", OrderID: "order-1", CustomerID: "customer-1",
			AmountCents: 12900, OccurredAt: contractTime(),
		}, fields)
	})
}

func TestNotificationRequestedV1JSONContract(t *testing.T) {
	assertJSONFields(t, event.NotificationRequestedV1{
		NotificationID: "notification-1",
		PaymentID:      "payment-1",
		OrderID:        "order-1",
		CustomerID:     "customer-1",
		RecipientID:    "customer-1",
		EventType:      event.NotificationPaymentSucceededV1,
		AmountCents:    12900,
		OccurredAt:     contractTime(),
	}, map[string]string{
		"notification_id": `"notification-1"`,
		"payment_id":      `"payment-1"`,
		"order_id":        `"order-1"`,
		"customer_id":     `"customer-1"`,
		"recipient_id":    `"customer-1"`,
		"event_type":      `"payment_succeeded"`,
		"amount_cents":    `12900`,
		"occurred_at":     `"2026-01-02T03:04:05Z"`,
	})
}

func TestOrderCreatedV1AcceptsAdditiveUnknownFields(t *testing.T) {
	payload := []byte(`{
		"order_id":"order-1",
		"customer_id":"customer-1",
		"product_id":"product-1",
		"quantity":2,
		"total_cents":12900,
		"occurred_at":"2026-01-02T03:04:05Z",
		"future_field":"ignored-by-v1"
	}`)

	var decoded event.OrderCreatedV1
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode additive event field: %v", err)
	}
	if decoded.OrderID != "order-1" || decoded.TotalCents != 12900 {
		t.Fatalf("decoded event changed unexpectedly: %#v", decoded)
	}
}

func TestPaymentEventsDoNotExposeProviderDetails(t *testing.T) {
	payloads := []any{
		event.PaymentSucceededV1{PaymentID: "payment-1", OrderID: "order-1", CustomerID: "customer-1", AmountCents: 12900, OccurredAt: contractTime()},
		event.PaymentFailedV1{PaymentID: "payment-1", OrderID: "order-1", CustomerID: "customer-1", AmountCents: 12900, OccurredAt: contractTime()},
	}
	for _, payload := range payloads {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payment event: %v", err)
		}
		for _, forbidden := range []string{"provider_reference", "failure_reason", "raw_response"} {
			if containsJSONKey(encoded, forbidden) {
				t.Fatalf("payment event exposes forbidden field %q: %s", forbidden, encoded)
			}
		}
	}
}

func assertJSONFields(t *testing.T, value any, want map[string]string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode marshalled event: %v", err)
	}

	gotKeys := make([]string, 0, len(fields))
	for key := range fields {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	wantKeys := make([]string, 0, len(want))
	for key := range want {
		wantKeys = append(wantKeys, key)
	}
	sort.Strings(wantKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("event fields changed: got %v, want %v; payload=%s", gotKeys, wantKeys, encoded)
	}
	for key, expected := range want {
		if string(fields[key]) != expected {
			t.Errorf("field %q changed: got %s, want %s", key, fields[key], expected)
		}
	}
}

func containsJSONKey(payload []byte, key string) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return false
	}
	_, found := fields[key]
	return found
}

func contractTime() time.Time {
	return time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
}
