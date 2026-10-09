package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	notificationapp "example.com/order-system/internal/application/notification"
	domainnotification "example.com/order-system/internal/domain/notification"
)

var (
	ErrDuplicateNotificationID      = errors.New("duplicate notification ID")
	ErrDuplicateNotificationOrderID = errors.New("duplicate notification order ID")
)

// NotificationStore 用 copy-on-write 模拟通知模块的 Inbox、通知记录和任务事务。
// 它与支付 Store 分开，体现通知模块拥有自己的数据边界。
type NotificationStore struct {
	mu sync.RWMutex

	inboxMessages         map[string]inboxMessage
	webhookEvents         map[string]memoryNotificationWebhookEvent
	notificationsByID     map[string]domainnotification.Notification
	notificationIDByOrder map[string]string
	tasks                 map[string]memoryNotificationTask
}

type memoryNotificationWebhookEvent struct {
	notificationID string
	payloadHash    string
	receivedAt     time.Time
	processedAt    time.Time
}

type memoryNotificationTask struct {
	notificationapp.Task
	nextAttemptAt time.Time
	lockedUntil   time.Time
	completedAt   time.Time
	lastError     string
}

func NewNotificationStore() *NotificationStore {
	return &NotificationStore{
		inboxMessages:         make(map[string]inboxMessage),
		webhookEvents:         make(map[string]memoryNotificationWebhookEvent),
		notificationsByID:     make(map[string]domainnotification.Notification),
		notificationIDByOrder: make(map[string]string),
		tasks:                 make(map[string]memoryNotificationTask),
	}
}

// TransactWebhook 使用与消息消费相同的 copy-on-write 方式模拟数据库事务。
// 回调处理中任一步失败时，事件记录、通知和任务都不会部分提交。
func (s *NotificationStore) TransactWebhook(ctx context.Context, fn func(notificationapp.WebhookTransaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &memoryNotificationWebhookTransaction{
		webhookEvents:     cloneMap(s.webhookEvents),
		notificationsByID: cloneMap(s.notificationsByID),
		tasks:             cloneMap(s.tasks),
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.webhookEvents, s.notificationsByID, s.tasks = tx.webhookEvents, tx.notificationsByID, tx.tasks
	return nil
}

type memoryNotificationWebhookTransaction struct {
	webhookEvents     map[string]memoryNotificationWebhookEvent
	notificationsByID map[string]domainnotification.Notification
	tasks             map[string]memoryNotificationTask
}

func (tx *memoryNotificationWebhookTransaction) LockNotification(notificationID, idempotencyKey string) (domainnotification.Notification, error) {
	notificationID, idempotencyKey = strings.TrimSpace(notificationID), strings.TrimSpace(idempotencyKey)
	current, exists := tx.notificationsByID[notificationID]
	if !exists {
		return domainnotification.Notification{}, domainnotification.ErrNotificationNotFound
	}
	for _, task := range tx.tasks {
		if task.Notification.ID() == notificationID && task.IdempotencyKey == idempotencyKey {
			return current, nil
		}
	}
	return domainnotification.Notification{}, domainnotification.ErrNotificationNotFound
}

func (tx *memoryNotificationWebhookTransaction) TryBeginWebhookEvent(eventID, notificationID, payloadHash string, receivedAt time.Time) (bool, error) {
	if existing, exists := tx.webhookEvents[eventID]; exists {
		if existing.notificationID != notificationID || existing.payloadHash != payloadHash {
			return false, notificationapp.ErrWebhookEventConflict
		}
		return false, nil
	}
	tx.webhookEvents[eventID] = memoryNotificationWebhookEvent{
		notificationID: notificationID, payloadHash: payloadHash,
		receivedAt: receivedAt.UTC(), processedAt: receivedAt.UTC(),
	}
	return true, nil
}

func (tx *memoryNotificationWebhookTransaction) CompleteNotification(updated domainnotification.Notification, reason string, completedAt time.Time) error {
	for taskID, task := range tx.tasks {
		if task.Notification.ID() != updated.ID() {
			continue
		}
		tx.notificationsByID[updated.ID()] = updated
		task.Notification, task.lastError = updated, strings.TrimSpace(reason)
		if task.completedAt.IsZero() {
			task.completedAt = completedAt.UTC()
		}
		task.LeaseID, task.lockedUntil = "", time.Time{}
		tx.tasks[taskID] = task
		return nil
	}
	return notificationapp.ErrInvalidTask
}

func (s *NotificationStore) Transact(ctx context.Context, fn func(notificationapp.Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &memoryNotificationTransaction{
		inboxMessages:         cloneMap(s.inboxMessages),
		notificationsByID:     cloneMap(s.notificationsByID),
		notificationIDByOrder: cloneMap(s.notificationIDByOrder),
		tasks:                 cloneMap(s.tasks),
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.inboxMessages = tx.inboxMessages
	s.notificationsByID = tx.notificationsByID
	s.notificationIDByOrder = tx.notificationIDByOrder
	s.tasks = tx.tasks
	return nil
}

type memoryNotificationTransaction struct {
	inboxMessages         map[string]inboxMessage
	notificationsByID     map[string]domainnotification.Notification
	notificationIDByOrder map[string]string
	tasks                 map[string]memoryNotificationTask
}

func (tx *memoryNotificationTransaction) TryBeginMessage(messageID, topic, payloadHash string, processedAt time.Time) (bool, error) {
	messageID, topic, payloadHash = strings.TrimSpace(messageID), strings.TrimSpace(topic), strings.TrimSpace(payloadHash)
	if messageID == "" || topic == "" || len(payloadHash) != 64 || processedAt.IsZero() {
		return false, fmt.Errorf("invalid inbox message")
	}
	if existing, exists := tx.inboxMessages[messageID]; exists {
		if existing.topic != topic || existing.payloadHash != payloadHash {
			return false, notificationapp.ErrInboxMessageConflict
		}
		return false, nil
	}
	tx.inboxMessages[messageID] = inboxMessage{topic: topic, payloadHash: payloadHash, processedAt: processedAt.UTC()}
	return true, nil
}

func (tx *memoryNotificationTransaction) SaveNotification(created domainnotification.Notification) error {
	if _, exists := tx.notificationsByID[created.ID()]; exists {
		return ErrDuplicateNotificationID
	}
	if _, exists := tx.notificationIDByOrder[created.OrderID()]; exists {
		return ErrDuplicateNotificationOrderID
	}
	tx.notificationsByID[created.ID()] = created
	tx.notificationIDByOrder[created.OrderID()] = created.ID()
	return nil
}

func (tx *memoryNotificationTransaction) SaveTask(task notificationapp.Task) error {
	if _, exists := tx.tasks[task.ID]; exists {
		return fmt.Errorf("duplicate notification task ID %q", task.ID)
	}
	if _, exists := tx.notificationsByID[task.Notification.ID()]; !exists {
		return domainnotification.ErrNotificationNotFound
	}
	tx.tasks[task.ID] = memoryNotificationTask{Task: task, nextAttemptAt: task.Notification.CreatedAt()}
	return nil
}

func (s *NotificationStore) GetNotificationByOrderIDAndCustomerID(ctx context.Context, orderID, customerID string) (domainnotification.Notification, error) {
	if err := ctx.Err(); err != nil {
		return domainnotification.Notification{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, exists := s.notificationIDByOrder[strings.TrimSpace(orderID)]
	if !exists {
		return domainnotification.Notification{}, domainnotification.ErrNotificationNotFound
	}
	got := s.notificationsByID[id]
	if got.CustomerID() != strings.TrimSpace(customerID) {
		return domainnotification.Notification{}, domainnotification.ErrNotificationNotFound
	}
	return got, nil
}

func (s *NotificationStore) ClaimTasks(ctx context.Context, leaseID string, batchSize int, now, lockedUntil time.Time) ([]notificationapp.Task, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(leaseID) == "" || batchSize <= 0 || !lockedUntil.After(now) {
		return nil, notificationapp.ErrInvalidTaskClaim
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, notifications := cloneMap(s.tasks), cloneMap(s.notificationsByID)
	eligible := make([]string, 0, len(tasks))
	for id, task := range tasks {
		if !task.completedAt.IsZero() || task.nextAttemptAt.After(now) || (!task.lockedUntil.IsZero() && task.lockedUntil.After(now)) {
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
	claimed := make([]notificationapp.Task, 0, len(eligible))
	for _, id := range eligible {
		record := tasks[id]
		current := notifications[record.Notification.ID()]
		needsReconciliation := current.Status() != domainnotification.StatusPending
		processing, err := current.StartProcessing(now)
		if err != nil {
			return nil, fmt.Errorf("start notification %q: %w", current.ID(), err)
		}
		notifications[current.ID()] = processing
		record.Notification, record.Attempts = processing, record.Attempts+1
		record.NeedsReconciliation = needsReconciliation
		record.LeaseID, record.lockedUntil = leaseID, lockedUntil
		tasks[id] = record
		claimed = append(claimed, record.Task)
	}
	s.tasks, s.notificationsByID = tasks, notifications
	return claimed, nil
}

func (s *NotificationStore) CompleteTask(ctx context.Context, taskID, leaseID string, outcome notificationapp.TaskOutcome, completedAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.tasks[taskID]
	if !exists || record.LeaseID != leaseID || !record.completedAt.IsZero() || record.Notification.ID() != outcome.Notification.ID() {
		return notificationapp.ErrTaskLeaseLost
	}
	if outcome.Notification.Status() != domainnotification.StatusSent && outcome.Notification.Status() != domainnotification.StatusFailed {
		return notificationapp.ErrInvalidTask
	}
	s.notificationsByID[outcome.Notification.ID()] = outcome.Notification
	record.Notification, record.completedAt = outcome.Notification, completedAt.UTC()
	record.LeaseID, record.lockedUntil, record.lastError = "", time.Time{}, outcome.Reason
	s.tasks[taskID] = record
	return nil
}

func (s *NotificationStore) RetryTask(ctx context.Context, taskID, leaseID string, outcome notificationapp.TaskOutcome, nextAttemptAt time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.tasks[taskID]
	if !exists || record.LeaseID != leaseID || !record.completedAt.IsZero() || record.Notification.ID() != outcome.Notification.ID() {
		return notificationapp.ErrTaskLeaseLost
	}
	if outcome.Notification.Status() != domainnotification.StatusUnknown {
		return notificationapp.ErrInvalidTask
	}
	s.notificationsByID[outcome.Notification.ID()] = outcome.Notification
	record.Notification, record.nextAttemptAt = outcome.Notification, nextAttemptAt.UTC()
	record.LeaseID, record.lockedUntil, record.lastError = "", time.Time{}, outcome.Reason
	s.tasks[taskID] = record
	return nil
}
