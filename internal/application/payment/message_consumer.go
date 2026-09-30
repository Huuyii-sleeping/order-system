// Package paymentapp 编排支付事件消费和支付任务执行。
package paymentapp

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
	"example.com/order-system/internal/domain/payment"
)

var (
	ErrInvalidOrderCreatedEvent = errors.New("invalid order created event")
	ErrInboxMessageConflict     = inbox.ErrMessageConflict
	ErrInvalidTask              = errors.New("invalid payment task")
	ErrInvalidTaskClaim         = errors.New("invalid payment task claim")
	ErrTaskLeaseLost            = errors.New("payment task lease was lost")
)

// Transaction 是消费一条订单事件时的本地事务边界。
// Inbox 标记、Payment 和 PaymentTask 必须在同一个事务中提交。
type Transaction interface {
	TryBeginMessage(messageID, topic, payloadHash string, processedAt time.Time) (bool, error)
	SavePayment(payment.Payment) error
	SaveTask(Task) error
}

type Store interface {
	Transact(context.Context, func(Transaction) error) error
	GetPaymentByOrderID(context.Context, string) (payment.Payment, error)
}

type IDGenerator interface {
	NewID() (string, error)
}

type Clock interface {
	Now() time.Time
}

type Task struct {
	ID             string
	Payment        payment.Payment
	IdempotencyKey string
	Attempts       int
	LeaseID        string
}

func NewTask(id string, created payment.Payment) (Task, error) {
	id = strings.TrimSpace(id)
	if id == "" || created.ID() == "" || created.Status() != payment.StatusPending {
		return Task{}, ErrInvalidTask
	}
	// 支付 ID 在所有重试中保持不变，正好可作为调用第三方的稳定幂等键。
	return Task{ID: id, Payment: created, IdempotencyKey: created.ID()}, nil
}

type Consumer struct {
	store Store
	ids   IDGenerator
	clock Clock
}

func NewConsumer(store Store, ids IDGenerator, clock Clock) *Consumer {
	return &Consumer{store: store, ids: ids, clock: clock}
}

type ConsumeResult struct {
	Payment   payment.Payment
	Processed bool
}

// Handle 让 Consumer 可以直接作为消息适配器的 Handler。只有本地事务提交成功，
// 才返回 nil，消息系统随后才能确认这次投递。
func (c *Consumer) Handle(ctx context.Context, message outbox.Message) error {
	_, err := c.Consume(ctx, message)
	return err
}

func (c *Consumer) Consume(ctx context.Context, message outbox.Message) (ConsumeResult, error) {
	event, err := decodeOrderCreated(message)
	if err != nil {
		return ConsumeResult{}, err
	}

	var result ConsumeResult
	err = c.store.Transact(ctx, func(tx Transaction) error {
		payloadHash := fmt.Sprintf("%x", sha256.Sum256(message.Payload))
		begun, err := tx.TryBeginMessage(
			message.ID, message.Topic, payloadHash, c.clock.Now(),
		)
		if err != nil {
			return fmt.Errorf("record inbox message: %w", err)
		}
		if !begun {
			result.Processed = false
			return nil
		}

		paymentID, err := c.ids.NewID()
		if err != nil {
			return fmt.Errorf("generate payment ID: %w", err)
		}
		created, err := payment.New(
			paymentID, event.OrderID, event.CustomerID, event.TotalCents, c.clock.Now(),
		)
		if err != nil {
			return fmt.Errorf("create payment: %w", err)
		}
		taskID, err := c.ids.NewID()
		if err != nil {
			return fmt.Errorf("generate payment task ID: %w", err)
		}
		task, err := NewTask(taskID, created)
		if err != nil {
			return fmt.Errorf("create payment task: %w", err)
		}

		if err := tx.SavePayment(created); err != nil {
			return fmt.Errorf("save payment: %w", err)
		}
		if err := tx.SaveTask(task); err != nil {
			return fmt.Errorf("save payment task: %w", err)
		}
		result = ConsumeResult{Payment: created, Processed: true}
		return nil
	})
	if err != nil {
		return ConsumeResult{}, fmt.Errorf("consume order created event: %w", err)
	}
	if !result.Processed {
		existing, err := c.store.GetPaymentByOrderID(ctx, event.OrderID)
		if err != nil {
			return ConsumeResult{}, fmt.Errorf("get payment for duplicate message: %w", err)
		}
		result.Payment = existing
	}
	return result, nil
}

func decodeOrderCreated(message outbox.Message) (event.OrderCreatedV1, error) {
	if message.Topic != event.OrderCreatedV1Topic || strings.TrimSpace(message.ID) == "" {
		return event.OrderCreatedV1{}, ErrInvalidOrderCreatedEvent
	}
	var payload event.OrderCreatedV1
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return event.OrderCreatedV1{}, fmt.Errorf("%w: %v", ErrInvalidOrderCreatedEvent, err)
	}
	payload.OrderID = strings.TrimSpace(payload.OrderID)
	payload.CustomerID = strings.TrimSpace(payload.CustomerID)
	payload.ProductID = strings.TrimSpace(payload.ProductID)
	if payload.OrderID == "" || message.Key != payload.OrderID || payload.CustomerID == "" ||
		payload.ProductID == "" || payload.Quantity <= 0 || payload.TotalCents <= 0 ||
		payload.OccurredAt.IsZero() {
		return event.OrderCreatedV1{}, ErrInvalidOrderCreatedEvent
	}
	return payload, nil
}
