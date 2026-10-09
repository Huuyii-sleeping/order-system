// Package notification 定义通知投递的领域状态和状态转换规则。
package notification

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidNotification      = errors.New("invalid notification")
	ErrInvalidNotificationState = errors.New("invalid notification state transition")
	ErrNotificationNotFound     = errors.New("notification not found")
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSent       Status = "sent"
	StatusFailed     Status = "failed"
	StatusUnknown    Status = "unknown"
)

// Notification 是一次业务通知的投递记录。第三方网络调用不在领域对象内执行，
// 它由 NotificationTask 驱动；领域对象只约束可持久化的状态变化。
type Notification struct {
	id                string
	paymentID         string
	orderID           string
	customerID        string
	recipientID       string
	eventType         string
	amountCents       int64
	status            Status
	providerReference string
	failureReason     string
	createdAt         time.Time
	updatedAt         time.Time
}

func New(id, paymentID, orderID, customerID, recipientID, eventType string, amountCents int64, createdAt time.Time) (Notification, error) {
	return Restore(id, paymentID, orderID, customerID, recipientID, eventType, amountCents, StatusPending, "", "", createdAt, createdAt)
}

func Restore(id, paymentID, orderID, customerID, recipientID, eventType string, amountCents int64, status Status, providerReference, failureReason string, createdAt, updatedAt time.Time) (Notification, error) {
	values := []*string{&id, &paymentID, &orderID, &customerID, &recipientID, &eventType, &providerReference, &failureReason}
	for _, value := range values {
		*value = strings.TrimSpace(*value)
	}
	if id == "" || paymentID == "" || orderID == "" || customerID == "" || recipientID == "" || eventType == "" || amountCents <= 0 || createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) || !status.valid() {
		return Notification{}, ErrInvalidNotification
	}
	if status == StatusSent && providerReference == "" {
		return Notification{}, ErrInvalidNotification
	}
	if status != StatusSent && providerReference != "" {
		return Notification{}, ErrInvalidNotification
	}
	if (status == StatusFailed || status == StatusUnknown) != (failureReason != "") {
		return Notification{}, ErrInvalidNotification
	}
	return Notification{id: id, paymentID: paymentID, orderID: orderID, customerID: customerID, recipientID: recipientID, eventType: eventType, amountCents: amountCents, status: status, providerReference: providerReference, failureReason: failureReason, createdAt: createdAt.UTC(), updatedAt: updatedAt.UTC()}, nil
}

func (n Notification) StartProcessing(at time.Time) (Notification, error) {
	if n.status != StatusPending && n.status != StatusUnknown && n.status != StatusProcessing {
		return Notification{}, ErrInvalidNotificationState
	}
	if err := n.validateTransitionTime(at); err != nil {
		return Notification{}, err
	}
	n.status, n.failureReason, n.updatedAt = StatusProcessing, "", at.UTC()
	return n, nil
}

func (n Notification) MarkSent(providerReference string, at time.Time) (Notification, error) {
	providerReference = strings.TrimSpace(providerReference)
	if n.status != StatusProcessing || providerReference == "" {
		return Notification{}, ErrInvalidNotificationState
	}
	if err := n.validateTransitionTime(at); err != nil {
		return Notification{}, err
	}
	n.status, n.providerReference, n.failureReason, n.updatedAt = StatusSent, providerReference, "", at.UTC()
	return n, nil
}

func (n Notification) MarkFailed(reason string, at time.Time) (Notification, error) {
	reason = strings.TrimSpace(reason)
	if n.status != StatusProcessing || reason == "" {
		return Notification{}, ErrInvalidNotificationState
	}
	if err := n.validateTransitionTime(at); err != nil {
		return Notification{}, err
	}
	n.status, n.failureReason, n.updatedAt = StatusFailed, reason, at.UTC()
	return n, nil
}

func (n Notification) MarkUnknown(reason string, at time.Time) (Notification, error) {
	reason = strings.TrimSpace(reason)
	if n.status != StatusProcessing || reason == "" {
		return Notification{}, ErrInvalidNotificationState
	}
	if err := n.validateTransitionTime(at); err != nil {
		return Notification{}, err
	}
	n.status, n.failureReason, n.updatedAt = StatusUnknown, reason, at.UTC()
	return n, nil
}

// ConfirmSent 接收 Provider 主动返回的成功终态。Webhook 可能早于同步 Send
// 响应到达，也可能在本地状态进入 unknown 后到达，因此它允许从所有非终态
// 收敛；已经是相同 sent 结果时按幂等成功处理，但绝不覆盖相反的终态。
func (n Notification) ConfirmSent(providerReference string, at time.Time) (Notification, bool, error) {
	providerReference = strings.TrimSpace(providerReference)
	if providerReference == "" {
		return Notification{}, false, ErrInvalidNotificationState
	}
	if n.status == StatusSent {
		if n.providerReference != providerReference {
			return Notification{}, false, ErrInvalidNotificationState
		}
		return n, false, nil
	}
	if n.status == StatusFailed {
		return Notification{}, false, ErrInvalidNotificationState
	}
	if err := n.validateTransitionTime(at); err != nil {
		return Notification{}, false, err
	}
	n.status, n.providerReference, n.failureReason, n.updatedAt = StatusSent, providerReference, "", at.UTC()
	return n, true, nil
}

// ConfirmFailed 与 ConfirmSent 对称：Provider 的明确拒绝可以收敛 pending、
// processing 或 unknown，但不允许把已经成功的通知倒退为失败。
func (n Notification) ConfirmFailed(reason string, at time.Time) (Notification, bool, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Notification{}, false, ErrInvalidNotificationState
	}
	if n.status == StatusFailed {
		if n.failureReason != reason {
			return Notification{}, false, ErrInvalidNotificationState
		}
		return n, false, nil
	}
	if n.status == StatusSent {
		return Notification{}, false, ErrInvalidNotificationState
	}
	if err := n.validateTransitionTime(at); err != nil {
		return Notification{}, false, err
	}
	n.status, n.providerReference, n.failureReason, n.updatedAt = StatusFailed, "", reason, at.UTC()
	return n, true, nil
}

func (n Notification) validateTransitionTime(at time.Time) error {
	if at.IsZero() || at.Before(n.updatedAt) {
		return ErrInvalidNotificationState
	}
	return nil
}

func (s Status) valid() bool {
	switch s {
	case StatusPending, StatusProcessing, StatusSent, StatusFailed, StatusUnknown:
		return true
	default:
		return false
	}
}

func (n Notification) ID() string                { return n.id }
func (n Notification) PaymentID() string         { return n.paymentID }
func (n Notification) OrderID() string           { return n.orderID }
func (n Notification) CustomerID() string        { return n.customerID }
func (n Notification) RecipientID() string       { return n.recipientID }
func (n Notification) EventType() string         { return n.eventType }
func (n Notification) AmountCents() int64        { return n.amountCents }
func (n Notification) Status() Status            { return n.status }
func (n Notification) ProviderReference() string { return n.providerReference }
func (n Notification) FailureReason() string     { return n.failureReason }
func (n Notification) CreatedAt() time.Time      { return n.createdAt }
func (n Notification) UpdatedAt() time.Time      { return n.updatedAt }
