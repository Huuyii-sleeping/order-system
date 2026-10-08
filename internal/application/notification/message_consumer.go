// Package notification 编排通知请求的消费和通知任务执行。
package notification

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
	domainnotification "example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/platform/telemetry"
)

var (
	ErrInvalidNotificationEvent = errors.New("invalid notification event")
	ErrInboxMessageConflict     = inbox.ErrMessageConflict
	ErrInvalidTask              = errors.New("invalid notification task")
	ErrInvalidTaskClaim         = errors.New("invalid notification task claim")
	ErrTaskLeaseLost            = errors.New("notification task lease was lost")
)

// Transaction 是消费通知事件的本地事务边界。Inbox、通知记录和任务必须一起提交。
type Transaction interface {
	TryBeginMessage(messageID, topic, payloadHash string, processedAt time.Time) (bool, error)
	SaveNotification(domainnotification.Notification) error
	SaveTask(Task) error
}

type Store interface {
	Transact(context.Context, func(Transaction) error) error
	GetNotificationByOrderIDAndCustomerID(context.Context, string, string) (domainnotification.Notification, error)
}

type IDGenerator interface{ NewID() (string, error) }
type Clock interface{ Now() time.Time }

type Task struct {
	ID             string
	Notification   domainnotification.Notification
	IdempotencyKey string
	Attempts       int
	LeaseID        string
	TraceContext   telemetry.PropagationContext
}

func NewTask(id string, created domainnotification.Notification) (Task, error) {
	if strings.TrimSpace(id) == "" || created.ID() == "" || created.Status() != domainnotification.StatusPending {
		return Task{}, ErrInvalidTask
	}
	// 通知 ID 在每次重试中保持不变，第三方 Provider 也必须使用这个键去重。
	return Task{ID: strings.TrimSpace(id), Notification: created, IdempotencyKey: created.ID()}, nil
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
	Notification domainnotification.Notification
	Processed    bool
}

func (c *Consumer) Handle(ctx context.Context, message outbox.Message) error {
	_, err := c.Consume(ctx, message)
	return err
}

func (c *Consumer) Consume(ctx context.Context, message outbox.Message) (ConsumeResult, error) {
	requested, err := decodeRequested(message)
	if err != nil {
		return ConsumeResult{}, err
	}
	var result ConsumeResult
	err = c.store.Transact(ctx, func(tx Transaction) error {
		payloadHash := fmt.Sprintf("%x", sha256.Sum256(message.Payload))
		begun, err := tx.TryBeginMessage(message.ID, message.Topic, payloadHash, c.clock.Now())
		if err != nil {
			return fmt.Errorf("record notification inbox message: %w", err)
		}
		if !begun {
			return nil
		}
		created, err := domainnotification.New(
			requested.NotificationID, requested.PaymentID, requested.OrderID,
			requested.CustomerID, requested.RecipientID, requested.EventType,
			requested.AmountCents, requested.OccurredAt,
		)
		if err != nil {
			return fmt.Errorf("create notification: %w", err)
		}
		taskID, err := c.ids.NewID()
		if err != nil {
			return fmt.Errorf("generate notification task ID: %w", err)
		}
		task, err := NewTask(taskID, created)
		if err != nil {
			return fmt.Errorf("create notification task: %w", err)
		}
		task.TraceContext = telemetry.Capture(ctx)
		if err := tx.SaveNotification(created); err != nil {
			return fmt.Errorf("save notification: %w", err)
		}
		if err := tx.SaveTask(task); err != nil {
			return fmt.Errorf("save notification task: %w", err)
		}
		result = ConsumeResult{Notification: created, Processed: true}
		return nil
	})
	if err != nil {
		return ConsumeResult{}, fmt.Errorf("consume notification event: %w", err)
	}
	if !result.Processed {
		existing, err := c.store.GetNotificationByOrderIDAndCustomerID(ctx, requested.OrderID, requested.CustomerID)
		if err != nil {
			return ConsumeResult{}, fmt.Errorf("get duplicate notification: %w", err)
		}
		result.Notification = existing
	}
	return result, nil
}

func decodeRequested(message outbox.Message) (event.NotificationRequestedV1, error) {
	if message.Topic != event.NotificationRequestedV1Topic || strings.TrimSpace(message.ID) == "" {
		return event.NotificationRequestedV1{}, ErrInvalidNotificationEvent
	}
	var payload event.NotificationRequestedV1
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		return event.NotificationRequestedV1{}, fmt.Errorf("%w: %v", ErrInvalidNotificationEvent, err)
	}
	payload.NotificationID = strings.TrimSpace(payload.NotificationID)
	payload.PaymentID = strings.TrimSpace(payload.PaymentID)
	payload.OrderID = strings.TrimSpace(payload.OrderID)
	payload.CustomerID = strings.TrimSpace(payload.CustomerID)
	payload.RecipientID = strings.TrimSpace(payload.RecipientID)
	payload.EventType = strings.TrimSpace(payload.EventType)
	if payload.NotificationID == "" || payload.PaymentID == "" || payload.OrderID == "" ||
		message.Key != payload.OrderID || payload.CustomerID == "" || payload.RecipientID == "" ||
		payload.AmountCents <= 0 || payload.OccurredAt.IsZero() ||
		(payload.EventType != event.NotificationPaymentSucceededV1 && payload.EventType != event.NotificationPaymentFailedV1) {
		return event.NotificationRequestedV1{}, ErrInvalidNotificationEvent
	}
	return payload, nil
}
