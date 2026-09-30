package orderapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/application/event"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

func TestPaymentResultConsumerConfirmsOrder(t *testing.T) {
	store, created := paymentResultFixture(t, 5, 2)
	consumer := orderapp.NewPaymentResultConsumer(store, clockStub{now: paymentResultTime()})
	message := paymentResultMessage(t, event.PaymentSucceededV1Topic, created)

	result, err := consumer.Consume(context.Background(), message)
	if err != nil || !result.Processed || result.Order.Status() != order.StatusConfirmed {
		t.Fatalf("Consume() = %+v, %v", result, err)
	}
	if got := readPaymentResultStock(t, store); got != 3 {
		t.Fatalf("available stock = %d, want reserved value 3", got)
	}
}

func TestPaymentResultConsumerCancelsOrderAndReleasesStockOnce(t *testing.T) {
	store, created := paymentResultFixture(t, 5, 2)
	consumer := orderapp.NewPaymentResultConsumer(store, clockStub{now: paymentResultTime()})
	message := paymentResultMessage(t, event.PaymentFailedV1Topic, created)

	first, err := consumer.Consume(context.Background(), message)
	if err != nil || !first.Processed || first.Order.Status() != order.StatusCancelled {
		t.Fatalf("first Consume() = %+v, %v", first, err)
	}
	second, err := consumer.Consume(context.Background(), message)
	if err != nil || second.Processed {
		t.Fatalf("duplicate Consume() = %+v, %v", second, err)
	}
	if got := readPaymentResultStock(t, store); got != 5 {
		t.Fatalf("available stock = %d, want restored value 5", got)
	}
}

func TestPaymentResultConsumerRejectsMismatchedOrderDataAndRollsBackInbox(t *testing.T) {
	store, created := paymentResultFixture(t, 5, 2)
	consumer := orderapp.NewPaymentResultConsumer(store, clockStub{now: paymentResultTime()})
	message := paymentResultMessage(t, event.PaymentSucceededV1Topic, created)

	var payload event.PaymentSucceededV1
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	payload.AmountCents++
	message.Payload, _ = json.Marshal(payload)
	if _, err := consumer.Consume(context.Background(), message); !errors.Is(err, orderapp.ErrPaymentResultMismatch) {
		t.Fatalf("Consume() error = %v, want mismatch", err)
	}

	// 事务失败时 Inbox 也必须回滚；修复消息后可以用同一 ID 重试。
	message = paymentResultMessage(t, event.PaymentSucceededV1Topic, created)
	result, err := consumer.Consume(context.Background(), message)
	if err != nil || !result.Processed || result.Order.Status() != order.StatusConfirmed {
		t.Fatalf("retry Consume() = %+v, %v", result, err)
	}
}

func TestPaymentResultConsumerRejectsMessageIDConflict(t *testing.T) {
	store, created := paymentResultFixture(t, 5, 1)
	consumer := orderapp.NewPaymentResultConsumer(store, clockStub{now: paymentResultTime()})
	message := paymentResultMessage(t, event.PaymentSucceededV1Topic, created)
	if _, err := consumer.Consume(context.Background(), message); err != nil {
		t.Fatalf("first Consume() error = %v", err)
	}
	message.Topic = event.PaymentFailedV1Topic
	message.Payload, _ = json.Marshal(event.PaymentFailedV1{
		PaymentID: "payment-1", OrderID: created.ID(), CustomerID: created.CustomerID(),
		AmountCents: created.TotalCents(), OccurredAt: paymentResultTime(),
	})
	if _, err := consumer.Consume(context.Background(), message); !errors.Is(err, orderapp.ErrInboxMessageConflict) {
		t.Fatalf("conflicting Consume() error = %v", err)
	}
}

func paymentResultFixture(
	t *testing.T, initialStock, quantity int,
) (*memory.OrderStore, order.Order) {
	t.Helper()
	stock, err := inventory.NewStock("product-1", initialStock)
	if err != nil {
		t.Fatalf("NewStock() error = %v", err)
	}
	store, err := memory.NewOrderStore([]inventory.Stock{stock})
	if err != nil {
		t.Fatalf("NewOrderStore() error = %v", err)
	}
	created, err := order.New(
		"order-1", "customer-1", "product-1", quantity, 12900, "key-1", paymentResultTime(),
	)
	if err != nil {
		t.Fatalf("order.New() error = %v", err)
	}
	if err := store.Transact(context.Background(), func(tx orderapp.Transaction) error {
		current, err := tx.GetStock(created.ProductID())
		if err != nil {
			return err
		}
		reserved, err := current.Reserve(created.Quantity())
		if err != nil {
			return err
		}
		if err := tx.SaveStock(reserved); err != nil {
			return err
		}
		return tx.SaveOrder(created)
	}); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return store, created
}

func paymentResultMessage(t *testing.T, topic string, created order.Order) outbox.Message {
	t.Helper()
	var payload []byte
	var err error
	switch topic {
	case event.PaymentSucceededV1Topic:
		payload, err = json.Marshal(event.PaymentSucceededV1{
			PaymentID: "payment-1", OrderID: created.ID(), CustomerID: created.CustomerID(),
			AmountCents: created.TotalCents(), OccurredAt: paymentResultTime(),
		})
	case event.PaymentFailedV1Topic:
		payload, err = json.Marshal(event.PaymentFailedV1{
			PaymentID: "payment-1", OrderID: created.ID(), CustomerID: created.CustomerID(),
			AmountCents: created.TotalCents(), OccurredAt: paymentResultTime(),
		})
	}
	if err != nil {
		t.Fatalf("marshal payment result: %v", err)
	}
	message, err := outbox.NewMessage(
		"payment-event-1", topic, created.ID(), payload, paymentResultTime(),
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	return message
}

func readPaymentResultStock(t *testing.T, store *memory.OrderStore) int {
	t.Helper()
	var available int
	if err := store.Transact(context.Background(), func(tx orderapp.Transaction) error {
		stock, err := tx.GetStock("product-1")
		if err != nil {
			return err
		}
		available = stock.Available()
		return nil
	}); err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return available
}

func paymentResultTime() time.Time {
	return time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
}
