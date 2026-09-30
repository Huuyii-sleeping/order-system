package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/domain/payment"
)

var (
	ErrDuplicatePaymentID      = errors.New("duplicate payment ID")
	ErrDuplicatePaymentOrderID = errors.New("duplicate payment order ID")
)

// PaymentStore 用 copy-on-write 模拟 Inbox、Payment、PaymentTask 的数据库事务。
type PaymentStore struct {
	mu sync.RWMutex

	inboxMessages    map[string]inboxMessage
	paymentsByID     map[string]payment.Payment
	paymentIDByOrder map[string]string
	tasks            map[string]memoryPaymentTask
}

type inboxMessage struct {
	topic       string
	payloadHash string
	processedAt time.Time
}

type memoryPaymentTask struct {
	paymentapp.Task
	nextAttemptAt time.Time
	lockedUntil   time.Time
	completedAt   time.Time
	lastError     string
}

func NewPaymentStore() *PaymentStore {
	return &PaymentStore{
		inboxMessages:    make(map[string]inboxMessage),
		paymentsByID:     make(map[string]payment.Payment),
		paymentIDByOrder: make(map[string]string),
		tasks:            make(map[string]memoryPaymentTask),
	}
}

func (s *PaymentStore) Transact(
	ctx context.Context, fn func(paymentapp.Transaction) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &memoryPaymentTransaction{
		inboxMessages:    cloneMap(s.inboxMessages),
		paymentsByID:     cloneMap(s.paymentsByID),
		paymentIDByOrder: cloneMap(s.paymentIDByOrder),
		tasks:            cloneMap(s.tasks),
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.inboxMessages = tx.inboxMessages
	s.paymentsByID = tx.paymentsByID
	s.paymentIDByOrder = tx.paymentIDByOrder
	s.tasks = tx.tasks
	return nil
}

type memoryPaymentTransaction struct {
	inboxMessages    map[string]inboxMessage
	paymentsByID     map[string]payment.Payment
	paymentIDByOrder map[string]string
	tasks            map[string]memoryPaymentTask
}

func (tx *memoryPaymentTransaction) TryBeginMessage(
	messageID, topic, payloadHash string, processedAt time.Time,
) (bool, error) {
	messageID = strings.TrimSpace(messageID)
	topic = strings.TrimSpace(topic)
	payloadHash = strings.TrimSpace(payloadHash)
	if messageID == "" || topic == "" || len(payloadHash) != 64 || processedAt.IsZero() {
		return false, fmt.Errorf("invalid inbox message")
	}
	if existing, exists := tx.inboxMessages[messageID]; exists {
		if existing.topic != topic || existing.payloadHash != payloadHash {
			return false, paymentapp.ErrInboxMessageConflict
		}
		return false, nil
	}
	tx.inboxMessages[messageID] = inboxMessage{
		topic: topic, payloadHash: payloadHash, processedAt: processedAt.UTC(),
	}
	return true, nil
}

func (tx *memoryPaymentTransaction) SavePayment(created payment.Payment) error {
	if _, exists := tx.paymentsByID[created.ID()]; exists {
		return ErrDuplicatePaymentID
	}
	if _, exists := tx.paymentIDByOrder[created.OrderID()]; exists {
		return ErrDuplicatePaymentOrderID
	}
	tx.paymentsByID[created.ID()] = created
	tx.paymentIDByOrder[created.OrderID()] = created.ID()
	return nil
}

func (tx *memoryPaymentTransaction) SaveTask(task paymentapp.Task) error {
	if _, exists := tx.tasks[task.ID]; exists {
		return fmt.Errorf("duplicate payment task ID %q", task.ID)
	}
	if _, exists := tx.paymentsByID[task.Payment.ID()]; !exists {
		return payment.ErrPaymentNotFound
	}
	tx.tasks[task.ID] = memoryPaymentTask{Task: task, nextAttemptAt: task.Payment.CreatedAt()}
	return nil
}

func (s *PaymentStore) GetPaymentByOrderID(
	ctx context.Context, orderID string,
) (payment.Payment, error) {
	if err := ctx.Err(); err != nil {
		return payment.Payment{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	paymentID, exists := s.paymentIDByOrder[orderID]
	if !exists {
		return payment.Payment{}, payment.ErrPaymentNotFound
	}
	return s.paymentsByID[paymentID], nil
}

func (s *PaymentStore) GetPaymentByOrderIDAndCustomerID(
	ctx context.Context, orderID, customerID string,
) (payment.Payment, error) {
	if err := ctx.Err(); err != nil {
		return payment.Payment{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	paymentID, exists := s.paymentIDByOrder[strings.TrimSpace(orderID)]
	if !exists {
		return payment.Payment{}, payment.ErrPaymentNotFound
	}
	got := s.paymentsByID[paymentID]
	if got.CustomerID() != strings.TrimSpace(customerID) {
		// 不区分“资源不存在”和“属于其他用户”，避免泄露支付是否存在。
		return payment.Payment{}, payment.ErrPaymentNotFound
	}
	return got, nil
}

func (s *PaymentStore) ClaimTasks(
	ctx context.Context, leaseID string, batchSize int, now, lockedUntil time.Time,
) ([]paymentapp.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(leaseID) == "" || batchSize <= 0 || !lockedUntil.After(now) {
		return nil, paymentapp.ErrInvalidTaskClaim
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tasks := cloneMap(s.tasks)
	payments := cloneMap(s.paymentsByID)
	eligible := make([]string, 0, len(tasks))
	for id, task := range tasks {
		if !task.completedAt.IsZero() || task.nextAttemptAt.After(now) ||
			(!task.lockedUntil.IsZero() && task.lockedUntil.After(now)) {
			continue
		}
		eligible = append(eligible, id)
	}
	sort.Slice(eligible, func(i, j int) bool {
		left, right := tasks[eligible[i]], tasks[eligible[j]]
		if left.nextAttemptAt.Equal(right.nextAttemptAt) {
			return eligible[i] < eligible[j]
		}
		return left.nextAttemptAt.Before(right.nextAttemptAt)
	})
	if len(eligible) > batchSize {
		eligible = eligible[:batchSize]
	}

	claimed := make([]paymentapp.Task, 0, len(eligible))
	for _, id := range eligible {
		record := tasks[id]
		current := payments[record.Payment.ID()]
		processing, err := current.StartProcessing(now)
		if err != nil {
			return nil, fmt.Errorf("start payment %q: %w", current.ID(), err)
		}
		payments[current.ID()] = processing
		record.Payment = processing
		record.Attempts++
		record.LeaseID = leaseID
		record.lockedUntil = lockedUntil
		tasks[id] = record
		claimed = append(claimed, record.Task)
	}
	s.tasks = tasks
	s.paymentsByID = payments
	return claimed, nil
}

func (s *PaymentStore) CompleteTask(
	ctx context.Context, taskID, leaseID string, outcome paymentapp.TaskOutcome, completedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.tasks[taskID]
	if !exists || record.LeaseID != leaseID || !record.completedAt.IsZero() ||
		record.Payment.ID() != outcome.Payment.ID() {
		return paymentapp.ErrTaskLeaseLost
	}
	if outcome.Payment.Status() != payment.StatusSucceeded && outcome.Payment.Status() != payment.StatusFailed {
		return paymentapp.ErrInvalidTask
	}
	s.paymentsByID[outcome.Payment.ID()] = outcome.Payment
	record.Payment = outcome.Payment
	record.completedAt = completedAt.UTC()
	record.LeaseID = ""
	record.lockedUntil = time.Time{}
	record.lastError = outcome.Reason
	s.tasks[taskID] = record
	return nil
}

func (s *PaymentStore) RetryTask(
	ctx context.Context, taskID, leaseID string, outcome paymentapp.TaskOutcome, nextAttemptAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.tasks[taskID]
	if !exists || record.LeaseID != leaseID || !record.completedAt.IsZero() ||
		record.Payment.ID() != outcome.Payment.ID() {
		return paymentapp.ErrTaskLeaseLost
	}
	if outcome.Payment.Status() != payment.StatusUnknown {
		return paymentapp.ErrInvalidTask
	}
	s.paymentsByID[outcome.Payment.ID()] = outcome.Payment
	record.Payment = outcome.Payment
	record.nextAttemptAt = nextAttemptAt.UTC()
	record.LeaseID = ""
	record.lockedUntil = time.Time{}
	record.lastError = outcome.Reason
	s.tasks[taskID] = record
	return nil
}
