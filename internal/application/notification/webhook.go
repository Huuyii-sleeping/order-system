package notification

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	domainnotification "example.com/order-system/internal/domain/notification"
)

var (
	ErrInvalidWebhookEvent   = errors.New("invalid notification webhook event")
	ErrWebhookEventConflict  = errors.New("notification webhook event conflict")
	ErrWebhookResultConflict = errors.New("notification webhook result conflicts with terminal state")
)

// WebhookTransaction 把回调去重记录、通知终态和调度任务终止放在同一个
// 本地事务里。只有三者一起提交，HTTP 才能安全地向 Provider 返回成功。
type WebhookTransaction interface {
	LockNotification(notificationID, idempotencyKey string) (domainnotification.Notification, error)
	TryBeginWebhookEvent(eventID, notificationID, payloadHash string, receivedAt time.Time) (bool, error)
	CompleteNotification(notification domainnotification.Notification, reason string, completedAt time.Time) error
}

type WebhookStore interface {
	TransactWebhook(context.Context, func(WebhookTransaction) error) error
}

type WebhookEvent struct {
	ID                string
	NotificationID    string
	IdempotencyKey    string
	Status            SendStatus
	ProviderReference string
	Reason            string
	PayloadHash       string
}

type WebhookResult struct {
	Notification domainnotification.Notification
	Processed    bool
}

type WebhookService struct {
	store WebhookStore
	clock Clock
}

func NewWebhookService(store WebhookStore, clock Clock) *WebhookService {
	return &WebhookService{store: store, clock: clock}
}

func (s *WebhookService) Process(ctx context.Context, event WebhookEvent) (WebhookResult, error) {
	event.ID = strings.TrimSpace(event.ID)
	event.NotificationID = strings.TrimSpace(event.NotificationID)
	event.IdempotencyKey = strings.TrimSpace(event.IdempotencyKey)
	event.ProviderReference = strings.TrimSpace(event.ProviderReference)
	event.Reason = strings.TrimSpace(event.Reason)
	event.PayloadHash = strings.TrimSpace(event.PayloadHash)
	if err := validateWebhookEvent(event); err != nil {
		return WebhookResult{}, err
	}

	receivedAt := s.clock.Now().UTC()
	var result WebhookResult
	err := s.store.TransactWebhook(ctx, func(tx WebhookTransaction) error {
		current, err := tx.LockNotification(event.NotificationID, event.IdempotencyKey)
		if err != nil {
			return err
		}
		begun, err := tx.TryBeginWebhookEvent(event.ID, event.NotificationID, event.PayloadHash, receivedAt)
		if err != nil {
			return err
		}
		if !begun {
			result = WebhookResult{Notification: current}
			return nil
		}

		updated, _, err := applyWebhookResult(current, event, receivedAt)
		if err != nil {
			return err
		}
		if err := tx.CompleteNotification(updated, updated.FailureReason(), receivedAt); err != nil {
			return fmt.Errorf("complete notification from webhook: %w", err)
		}
		result = WebhookResult{Notification: updated, Processed: true}
		return nil
	})
	if err != nil {
		return WebhookResult{}, fmt.Errorf("process notification webhook: %w", err)
	}
	return result, nil
}

func validateWebhookEvent(event WebhookEvent) error {
	if event.ID == "" || len(event.ID) > 200 ||
		event.NotificationID == "" || len(event.NotificationID) > 200 ||
		event.IdempotencyKey == "" || len(event.IdempotencyKey) > 200 ||
		len(event.PayloadHash) != 64 {
		return ErrInvalidWebhookEvent
	}
	if _, err := hex.DecodeString(event.PayloadHash); err != nil {
		return ErrInvalidWebhookEvent
	}
	switch event.Status {
	case SendSucceeded:
		if event.ProviderReference == "" || len(event.ProviderReference) > 500 || event.Reason != "" {
			return ErrInvalidWebhookEvent
		}
	case SendRejected:
		if event.Reason == "" || len(event.Reason) > 1000 || event.ProviderReference != "" {
			return ErrInvalidWebhookEvent
		}
	default:
		return ErrInvalidWebhookEvent
	}
	return nil
}

func applyWebhookResult(current domainnotification.Notification, event WebhookEvent, at time.Time) (domainnotification.Notification, bool, error) {
	// 不同实例的时钟可能有极小偏差。数据库行锁已经确定了状态更新顺序，
	// 因此回调时间落后于当前 updated_at 时使用当前值，避免可信终态因时钟漂移被拒绝。
	if at.Before(current.UpdatedAt()) {
		at = current.UpdatedAt()
	}
	var (
		updated domainnotification.Notification
		changed bool
		err     error
	)
	switch event.Status {
	case SendSucceeded:
		updated, changed, err = current.ConfirmSent(event.ProviderReference, at)
	case SendRejected:
		updated, changed, err = current.ConfirmFailed(event.Reason, at)
	}
	if errors.Is(err, domainnotification.ErrInvalidNotificationState) {
		return domainnotification.Notification{}, false, ErrWebhookResultConflict
	}
	return updated, changed, err
}
