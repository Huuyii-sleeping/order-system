package orderapp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/order-system/internal/application/event"
	"example.com/order-system/internal/application/inbox"
	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

var (
	ErrInvalidPaymentResultEvent = errors.New("invalid payment result event")
	ErrPaymentResultMismatch     = errors.New("payment result does not match order")
	ErrInboxMessageConflict      = inbox.ErrMessageConflict
)

// PaymentResultTransaction 是订单模块消费支付结果时的本地事务边界。
// Inbox、订单终态和失败补偿的库存必须一起提交或一起回滚。
type PaymentResultTransaction interface {
	TryBeginMessage(messageID, topic, payloadHash string, processedAt time.Time) (bool, error)
	GetOrderForUpdate(orderID string) (order.Order, error)
	GetStock(productID string) (inventory.Stock, error)
	SaveStock(inventory.Stock) error
	UpdateOrder(order.Order) error
}

type PaymentResultStore interface {
	TransactPaymentResult(context.Context, func(PaymentResultTransaction) error) error
}

type PaymentResultConsumer struct {
	store PaymentResultStore
	clock Clock
}

func NewPaymentResultConsumer(store PaymentResultStore, clock Clock) *PaymentResultConsumer {
	return &PaymentResultConsumer{store: store, clock: clock}
}

func (c *PaymentResultConsumer) Handle(ctx context.Context, message outbox.Message) error {
	_, err := c.Consume(ctx, message)
	return err
}

type PaymentResultConsumeResult struct {
	Order     order.Order
	Processed bool
}

// Consume 是一个事件驱动的 Saga 步骤：成功时确认订单；明确失败时取消订单
// 并释放库存。重复消息在 Inbox 命中后直接返回，不会再次执行补偿。
func (c *PaymentResultConsumer) Consume(
	ctx context.Context, message outbox.Message,
) (PaymentResultConsumeResult, error) {
	resultEvent, err := decodePaymentResult(message)
	if err != nil {
		return PaymentResultConsumeResult{}, err
	}

	var result PaymentResultConsumeResult
	err = c.store.TransactPaymentResult(ctx, func(tx PaymentResultTransaction) error {
		payloadHash := fmt.Sprintf("%x", sha256.Sum256(message.Payload))
		begun, err := tx.TryBeginMessage(
			message.ID, message.Topic, payloadHash, c.clock.Now(),
		)
		if err != nil {
			return fmt.Errorf("record inbox message: %w", err)
		}
		if !begun {
			return nil
		}

		current, err := tx.GetOrderForUpdate(resultEvent.OrderID)
		if err != nil {
			return fmt.Errorf("get order for payment result: %w", err)
		}
		if current.CustomerID() != resultEvent.CustomerID ||
			current.TotalCents() != resultEvent.AmountCents {
			return ErrPaymentResultMismatch
		}

		switch resultEvent.Topic {
		case event.PaymentSucceededV1Topic:
			current, err = current.Confirm()
			if err != nil {
				return fmt.Errorf("confirm order: %w", err)
			}
		case event.PaymentFailedV1Topic:
			current, err = current.Cancel()
			if err != nil {
				return fmt.Errorf("cancel order: %w", err)
			}
			stock, err := tx.GetStock(current.ProductID())
			if err != nil {
				return fmt.Errorf("get stock for compensation: %w", err)
			}
			released, err := stock.Release(current.Quantity())
			if err != nil {
				return fmt.Errorf("release stock: %w", err)
			}
			if err := tx.SaveStock(released); err != nil {
				return fmt.Errorf("save compensated stock: %w", err)
			}
		}
		if err := tx.UpdateOrder(current); err != nil {
			return fmt.Errorf("update order status: %w", err)
		}
		result = PaymentResultConsumeResult{Order: current, Processed: true}
		return nil
	})
	if err != nil {
		return PaymentResultConsumeResult{}, fmt.Errorf("consume payment result event: %w", err)
	}
	return result, nil
}

type paymentResult struct {
	Topic       string
	PaymentID   string
	OrderID     string
	CustomerID  string
	AmountCents int64
	OccurredAt  time.Time
}

func decodePaymentResult(message outbox.Message) (paymentResult, error) {
	if strings.TrimSpace(message.ID) == "" {
		return paymentResult{}, ErrInvalidPaymentResultEvent
	}

	var result paymentResult
	result.Topic = message.Topic
	switch message.Topic {
	case event.PaymentSucceededV1Topic:
		var payload event.PaymentSucceededV1
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			return paymentResult{}, fmt.Errorf("%w: %v", ErrInvalidPaymentResultEvent, err)
		}
		result.PaymentID, result.OrderID = payload.PaymentID, payload.OrderID
		result.CustomerID, result.AmountCents = payload.CustomerID, payload.AmountCents
		result.OccurredAt = payload.OccurredAt
	case event.PaymentFailedV1Topic:
		var payload event.PaymentFailedV1
		if err := json.Unmarshal(message.Payload, &payload); err != nil {
			return paymentResult{}, fmt.Errorf("%w: %v", ErrInvalidPaymentResultEvent, err)
		}
		result.PaymentID, result.OrderID = payload.PaymentID, payload.OrderID
		result.CustomerID, result.AmountCents = payload.CustomerID, payload.AmountCents
		result.OccurredAt = payload.OccurredAt
	default:
		return paymentResult{}, ErrInvalidPaymentResultEvent
	}

	result.PaymentID = strings.TrimSpace(result.PaymentID)
	result.OrderID = strings.TrimSpace(result.OrderID)
	result.CustomerID = strings.TrimSpace(result.CustomerID)
	if result.PaymentID == "" || result.OrderID == "" || message.Key != result.OrderID ||
		result.CustomerID == "" || result.AmountCents <= 0 || result.OccurredAt.IsZero() {
		return paymentResult{}, ErrInvalidPaymentResultEvent
	}
	return result, nil
}
