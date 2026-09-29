package outbox_test

import (
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/application/outbox"
)

func TestNewMessageValidatesStableEnvelope(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	message, err := outbox.NewMessage(" message-1 ", " orders.created.v1 ", " order-1 ", []byte(`{"ok":true}`), now)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if message.ID != "message-1" || message.Topic != "orders.created.v1" || message.Key != "order-1" {
		t.Errorf("message = %+v", message)
	}

	if _, err := outbox.NewMessage("", "topic", "key", []byte(`{}`), now); !errors.Is(err, outbox.ErrInvalidMessage) {
		t.Errorf("invalid message error = %v", err)
	}
}
