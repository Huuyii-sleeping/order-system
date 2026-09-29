package messaging_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/messaging"
	"example.com/order-system/internal/application/outbox"
)

type handlerStub struct {
	message outbox.Message
	err     error
}

func (h *handlerStub) Handle(_ context.Context, message outbox.Message) error {
	h.message = message
	return h.err
}

func TestDirectPublisherRoutesByTopic(t *testing.T) {
	handler := &handlerStub{}
	publisher := messaging.NewDirectPublisher(nil, map[string]messaging.MessageHandler{
		"orders.created.v1": handler,
	})
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	if err := publisher.Publish(context.Background(), message); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if handler.message.ID != message.ID {
		t.Fatalf("handled message = %+v", handler.message)
	}
}

func TestDirectPublisherReturnsHandlerFailure(t *testing.T) {
	wantErr := errors.New("consumer database unavailable")
	publisher := messaging.NewDirectPublisher(nil, map[string]messaging.MessageHandler{
		"orders.created.v1": &handlerStub{err: wantErr},
	})
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	if err := publisher.Publish(context.Background(), message); !errors.Is(err, wantErr) {
		t.Fatalf("Publish() error = %v, want %v", err, wantErr)
	}
}
