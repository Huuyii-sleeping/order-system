package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	notificationapp "example.com/order-system/internal/application/notification"
	domainnotification "example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/platform/telemetry"
)

// NotificationStore 是通知模块自己的持久化适配器。它只访问通知表、Inbox 表和任务表，
// 不通过 OrderStore 查询订单，保持模块边界。
type NotificationStore struct{ db *sql.DB }

func NewNotificationStore(db *sql.DB) *NotificationStore { return &NotificationStore{db: db} }

func (s *NotificationStore) Transact(ctx context.Context, fn func(notificationapp.Transaction) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin notification transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&notificationTransaction{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification transaction: %w", err)
	}
	return nil
}

type notificationTransaction struct {
	ctx context.Context
	tx  *sql.Tx
}

func (tx *notificationTransaction) TryBeginMessage(messageID, topic, payloadHash string, processedAt time.Time) (bool, error) {
	result, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO inbox_messages (message_id, topic, payload_hash, processed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (message_id) DO NOTHING
	`, messageID, topic, payloadHash, processedAt)
	if err != nil {
		return false, fmt.Errorf("insert notification inbox message: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read notification inbox rows: %w", err)
	}
	if rows == 1 {
		return true, nil
	}
	var existingTopic, existingPayloadHash string
	if err := tx.tx.QueryRowContext(tx.ctx, `SELECT topic, payload_hash FROM inbox_messages WHERE message_id = $1`, messageID).Scan(&existingTopic, &existingPayloadHash); err != nil {
		return false, fmt.Errorf("read duplicate notification inbox message: %w", err)
	}
	if existingTopic != topic || existingPayloadHash != payloadHash {
		return false, notificationapp.ErrInboxMessageConflict
	}
	return false, nil
}

func (tx *notificationTransaction) SaveNotification(created domainnotification.Notification) error {
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO notifications (
			id, payment_id, order_id, customer_id, recipient_id, event_type,
			amount_cents, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
	`, created.ID(), created.PaymentID(), created.OrderID(), created.CustomerID(), created.RecipientID(), created.EventType(), created.AmountCents(), created.Status(), created.CreatedAt())
	if err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}
	return nil
}

func (tx *notificationTransaction) SaveTask(task notificationapp.Task) error {
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO notification_tasks (
			id, notification_id, idempotency_key, next_attempt_at, trace_parent, trace_state
		) VALUES ($1, $2, $3, $4, $5, $6)
	`, task.ID, task.Notification.ID(), task.IdempotencyKey, task.Notification.CreatedAt(), nullableText(task.TraceContext.TraceParent), nullableText(task.TraceContext.TraceState))
	if err != nil {
		return fmt.Errorf("insert notification task: %w", err)
	}
	return nil
}

func (s *NotificationStore) GetNotificationByOrderIDAndCustomerID(ctx context.Context, orderID, customerID string) (domainnotification.Notification, error) {
	got, err := scanNotification(s.db.QueryRowContext(ctx, notificationSelect+` WHERE order_id = $1 AND customer_id = $2`, strings.TrimSpace(orderID), strings.TrimSpace(customerID)))
	if errors.Is(err, sql.ErrNoRows) {
		return domainnotification.Notification{}, domainnotification.ErrNotificationNotFound
	}
	if err != nil {
		return domainnotification.Notification{}, fmt.Errorf("get notification by order and customer ID: %w", err)
	}
	return got, nil
}

func (s *NotificationStore) ClaimTasks(ctx context.Context, leaseID string, batchSize int, now, lockedUntil time.Time) ([]notificationapp.Task, error) {
	if strings.TrimSpace(leaseID) == "" || batchSize <= 0 || !lockedUntil.After(now) {
		return nil, notificationapp.ErrInvalidTaskClaim
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin notification task claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT task.id, task.notification_id, task.idempotency_key, task.attempts,
		       task.trace_parent, task.trace_state,
		       n.payment_id, n.order_id, n.customer_id, n.recipient_id, n.event_type,
		       n.amount_cents, n.status, n.provider_reference, n.failure_reason,
		       n.created_at, n.updated_at
		FROM notification_tasks AS task
		JOIN notifications AS n ON n.id = task.notification_id
		WHERE task.completed_at IS NULL
		  AND task.next_attempt_at <= $1
		  AND (task.locked_until IS NULL OR task.locked_until <= $1)
		  AND n.status IN ('pending', 'processing', 'unknown')
		ORDER BY task.next_attempt_at, task.id
		LIMIT $2
		FOR UPDATE OF task SKIP LOCKED
	`, now, batchSize)
	if err != nil {
		return nil, fmt.Errorf("select notification tasks: %w", err)
	}
	defer rows.Close()
	tasks := make([]notificationapp.Task, 0, batchSize)
	for rows.Next() {
		var taskID, notificationID, idempotencyKey string
		var attempts int
		var traceParent, traceState sql.NullString
		var paymentID, orderID, customerID, recipientID, eventType, status string
		var amountCents int64
		var providerReference, failureReason sql.NullString
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&taskID, &notificationID, &idempotencyKey, &attempts, &traceParent, &traceState, &paymentID, &orderID, &customerID, &recipientID, &eventType, &amountCents, &status, &providerReference, &failureReason, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan notification task: %w", err)
		}
		current, err := domainnotification.Restore(notificationID, paymentID, orderID, customerID, recipientID, eventType, amountCents, domainnotification.Status(status), providerReference.String, failureReason.String, createdAt, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("restore notification %q: %w", notificationID, err)
		}
		needsReconciliation := current.Status() != domainnotification.StatusPending
		processing, err := current.StartProcessing(now)
		if err != nil {
			return nil, fmt.Errorf("start notification %q: %w", notificationID, err)
		}
		tasks = append(tasks, notificationapp.Task{ID: taskID, Notification: processing, IdempotencyKey: idempotencyKey, Attempts: attempts + 1, LeaseID: leaseID, NeedsReconciliation: needsReconciliation, TraceContext: telemetry.PropagationContext{TraceParent: traceParent.String, TraceState: traceState.String}})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification tasks: %w", err)
	}
	for _, task := range tasks {
		if _, err := tx.ExecContext(ctx, `UPDATE notifications SET status = $1, failure_reason = NULL, updated_at = $2 WHERE id = $3`, task.Notification.Status(), task.Notification.UpdatedAt(), task.Notification.ID()); err != nil {
			return nil, fmt.Errorf("mark notification processing: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_tasks SET attempts = attempts + 1, lease_id = $1, locked_until = $2 WHERE id = $3`, leaseID, lockedUntil, task.ID); err != nil {
			return nil, fmt.Errorf("lease notification task: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit notification task claim: %w", err)
	}
	return tasks, nil
}

func (s *NotificationStore) CompleteTask(ctx context.Context, taskID, leaseID string, outcome notificationapp.TaskOutcome, completedAt time.Time) error {
	if outcome.Notification.Status() != domainnotification.StatusSent && outcome.Notification.Status() != domainnotification.StatusFailed {
		return notificationapp.ErrInvalidTask
	}
	return s.updateTask(ctx, taskID, leaseID, outcome, completedAt, true)
}

func (s *NotificationStore) RetryTask(ctx context.Context, taskID, leaseID string, outcome notificationapp.TaskOutcome, nextAttemptAt time.Time) error {
	if outcome.Notification.Status() != domainnotification.StatusUnknown {
		return notificationapp.ErrInvalidTask
	}
	return s.updateTask(ctx, taskID, leaseID, outcome, nextAttemptAt, false)
}

func (s *NotificationStore) updateTask(ctx context.Context, taskID, leaseID string, outcome notificationapp.TaskOutcome, at time.Time, complete bool) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin notification task update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var notificationID string
	err = tx.QueryRowContext(ctx, `SELECT notification_id FROM notification_tasks WHERE id = $1 AND lease_id = $2 AND completed_at IS NULL FOR UPDATE`, taskID, leaseID).Scan(&notificationID)
	if errors.Is(err, sql.ErrNoRows) {
		return notificationapp.ErrTaskLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock notification task: %w", err)
	}
	if notificationID != outcome.Notification.ID() {
		return notificationapp.ErrTaskLeaseLost
	}
	result, err := tx.ExecContext(ctx, `UPDATE notifications SET status = $1, provider_reference = $2, failure_reason = $3, updated_at = $4 WHERE id = $5 AND status = 'processing'`, outcome.Notification.Status(), nullableText(outcome.Notification.ProviderReference()), nullableText(outcome.Notification.FailureReason()), outcome.Notification.UpdatedAt(), outcome.Notification.ID())
	if err != nil {
		return fmt.Errorf("update notification result: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read notification result rows: %w", err)
	}
	if rowsAffected != 1 {
		return notificationapp.ErrTaskLeaseLost
	}
	if complete {
		_, err = tx.ExecContext(ctx, `UPDATE notification_tasks SET completed_at = $1, lease_id = NULL, locked_until = NULL, last_error = $2 WHERE id = $3 AND lease_id = $4`, at, nullableText(outcome.Reason), taskID, leaseID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE notification_tasks SET next_attempt_at = $1, lease_id = NULL, locked_until = NULL, last_error = $2 WHERE id = $3 AND lease_id = $4`, at, nullableText(outcome.Reason), taskID, leaseID)
	}
	if err != nil {
		return fmt.Errorf("update notification task result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit notification task update: %w", err)
	}
	return nil
}

const notificationSelect = `
	SELECT id, payment_id, order_id, customer_id, recipient_id, event_type,
	       amount_cents, status, provider_reference, failure_reason, created_at, updated_at
	FROM notifications
`

type notificationScanner interface{ Scan(...any) error }

func scanNotification(scanner notificationScanner) (domainnotification.Notification, error) {
	var id, paymentID, orderID, customerID, recipientID, eventType, status string
	var amountCents int64
	var providerReference, failureReason sql.NullString
	var createdAt, updatedAt time.Time
	if err := scanner.Scan(&id, &paymentID, &orderID, &customerID, &recipientID, &eventType, &amountCents, &status, &providerReference, &failureReason, &createdAt, &updatedAt); err != nil {
		return domainnotification.Notification{}, err
	}
	return domainnotification.Restore(id, paymentID, orderID, customerID, recipientID, eventType, amountCents, domainnotification.Status(status), providerReference.String, failureReason.String, createdAt, updatedAt)
}
