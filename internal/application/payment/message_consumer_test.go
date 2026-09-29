package paymentapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/application/event"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
)

type sequenceIDs struct{ next atomic.Uint64 }

func (g *sequenceIDs) NewID() (string, error) {
	return fmt.Sprintf("payment-sequence-%d", g.next.Add(1)), nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestConsumerCreatesOnePaymentForDuplicateDelivery(t *testing.T) {
	store := memory.NewPaymentStore()
	consumer := paymentapp.NewConsumer(store, &sequenceIDs{}, fixedClock{now: testTime()})
	message := orderCreatedMessage(t, "message-1", "order-1")

	first, err := consumer.Consume(context.Background(), message)
	if err != nil || !first.Processed {
		t.Fatalf("first Consume() = %+v, %v", first, err)
	}
	second, err := consumer.Consume(context.Background(), message)
	if err != nil || second.Processed || second.Payment.ID() != first.Payment.ID() {
		t.Fatalf("duplicate Consume() = %+v, %v; first = %+v", second, err, first)
	}
}

func TestConsumerRejectsMessageIDReusedWithDifferentPayload(t *testing.T) {
	store := memory.NewPaymentStore()
	consumer := paymentapp.NewConsumer(store, &sequenceIDs{}, fixedClock{now: testTime()})
	if _, err := consumer.Consume(context.Background(), orderCreatedMessage(t, "message-1", "order-1")); err != nil {
		t.Fatalf("first Consume() error = %v", err)
	}
	_, err := consumer.Consume(context.Background(), orderCreatedMessage(t, "message-1", "order-2"))
	if !errors.Is(err, paymentapp.ErrInboxMessageConflict) {
		t.Fatalf("conflicting Consume() error = %v", err)
	}
}

func orderCreatedMessage(t *testing.T, messageID, orderID string) outbox.Message {
	t.Helper()
	payload, err := json.Marshal(event.OrderCreatedV1{
		OrderID: orderID, CustomerID: "customer-1", ProductID: "product-1",
		Quantity: 1, TotalCents: 12900, OccurredAt: testTime(),
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	message, err := outbox.NewMessage(messageID, orderapp.OrderCreatedTopic, orderID, payload, testTime())
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	return message
}

func testTime() time.Time {
	return time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
}
