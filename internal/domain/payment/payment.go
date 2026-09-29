// Package payment 定义支付聚合及其状态转换规则。
package payment

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidPayment      = errors.New("invalid payment")
	ErrInvalidPaymentState = errors.New("invalid payment state transition")
	ErrPaymentNotFound     = errors.New("payment not found")
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusUnknown    Status = "unknown"
)

// Payment 保存支付的业务状态。第三方调用本身由 PaymentTask 驱动，
// Payment 只负责约束哪些状态变化是合法的。
type Payment struct {
	id                string
	orderID           string
	customerID        string
	amountCents       int64
	status            Status
	providerReference string
	failureReason     string
	createdAt         time.Time
	updatedAt         time.Time
}

func New(
	id, orderID, customerID string, amountCents int64, createdAt time.Time,
) (Payment, error) {
	return Restore(
		id, orderID, customerID, amountCents, StatusPending, "", "",
		createdAt, createdAt,
	)
}

// Restore 从持久化数据恢复支付，同时验证数据库中的状态仍满足领域不变量。
func Restore(
	id, orderID, customerID string,
	amountCents int64,
	status Status,
	providerReference, failureReason string,
	createdAt, updatedAt time.Time,
) (Payment, error) {
	id = strings.TrimSpace(id)
	orderID = strings.TrimSpace(orderID)
	customerID = strings.TrimSpace(customerID)
	providerReference = strings.TrimSpace(providerReference)
	failureReason = strings.TrimSpace(failureReason)

	if id == "" || orderID == "" || customerID == "" || amountCents <= 0 ||
		createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) ||
		!status.valid() {
		return Payment{}, ErrInvalidPayment
	}
	if status == StatusSucceeded && providerReference == "" {
		return Payment{}, ErrInvalidPayment
	}
	if status != StatusSucceeded && providerReference != "" {
		return Payment{}, ErrInvalidPayment
	}
	if (status == StatusFailed || status == StatusUnknown) != (failureReason != "") {
		return Payment{}, ErrInvalidPayment
	}

	return Payment{
		id: id, orderID: orderID, customerID: customerID, amountCents: amountCents,
		status: status, providerReference: providerReference, failureReason: failureReason,
		createdAt: createdAt.UTC(), updatedAt: updatedAt.UTC(),
	}, nil
}

// StartProcessing 表示 Worker 已经取得处理权。processing 允许再次进入，
// 因为旧 Worker 崩溃、租约过期后，新 Worker 必须能够恢复同一任务。
func (p Payment) StartProcessing(at time.Time) (Payment, error) {
	if p.status != StatusPending && p.status != StatusUnknown && p.status != StatusProcessing {
		return Payment{}, ErrInvalidPaymentState
	}
	if err := p.validateTransitionTime(at); err != nil {
		return Payment{}, err
	}
	p.status = StatusProcessing
	p.failureReason = ""
	p.updatedAt = at.UTC()
	return p, nil
}

func (p Payment) Succeed(providerReference string, at time.Time) (Payment, error) {
	providerReference = strings.TrimSpace(providerReference)
	if p.status != StatusProcessing || providerReference == "" {
		return Payment{}, ErrInvalidPaymentState
	}
	if err := p.validateTransitionTime(at); err != nil {
		return Payment{}, err
	}
	p.status = StatusSucceeded
	p.providerReference = providerReference
	p.failureReason = ""
	p.updatedAt = at.UTC()
	return p, nil
}

func (p Payment) Fail(reason string, at time.Time) (Payment, error) {
	reason = strings.TrimSpace(reason)
	if p.status != StatusProcessing || reason == "" {
		return Payment{}, ErrInvalidPaymentState
	}
	if err := p.validateTransitionTime(at); err != nil {
		return Payment{}, err
	}
	p.status = StatusFailed
	p.failureReason = reason
	p.updatedAt = at.UTC()
	return p, nil
}

func (p Payment) MarkUnknown(reason string, at time.Time) (Payment, error) {
	reason = strings.TrimSpace(reason)
	if p.status != StatusProcessing || reason == "" {
		return Payment{}, ErrInvalidPaymentState
	}
	if err := p.validateTransitionTime(at); err != nil {
		return Payment{}, err
	}
	p.status = StatusUnknown
	p.failureReason = reason
	p.updatedAt = at.UTC()
	return p, nil
}

func (p Payment) validateTransitionTime(at time.Time) error {
	if at.IsZero() || at.Before(p.updatedAt) {
		return ErrInvalidPaymentState
	}
	return nil
}

func (s Status) valid() bool {
	switch s {
	case StatusPending, StatusProcessing, StatusSucceeded, StatusFailed, StatusUnknown:
		return true
	default:
		return false
	}
}

func (p Payment) ID() string                { return p.id }
func (p Payment) OrderID() string           { return p.orderID }
func (p Payment) CustomerID() string        { return p.customerID }
func (p Payment) AmountCents() int64        { return p.amountCents }
func (p Payment) Status() Status            { return p.status }
func (p Payment) ProviderReference() string { return p.providerReference }
func (p Payment) FailureReason() string     { return p.failureReason }
func (p Payment) CreatedAt() time.Time      { return p.createdAt }
func (p Payment) UpdatedAt() time.Time      { return p.updatedAt }
