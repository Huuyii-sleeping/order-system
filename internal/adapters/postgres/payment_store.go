package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/domain/payment"
)

type PaymentStore struct {
	db *sql.DB
}

func NewPaymentStore(db *sql.DB) *PaymentStore {
	return &PaymentStore{db: db}
}

func (s *PaymentStore) Transact(
	ctx context.Context, fn func(paymentapp.Transaction) error,
) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin payment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&paymentTransaction{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit payment transaction: %w", err)
	}
	return nil
}

type paymentTransaction struct {
	ctx context.Context
	tx  *sql.Tx
}

func (tx *paymentTransaction) TryBeginMessage(
	messageID, topic, payloadHash string, processedAt time.Time,
) (bool, error) {
	// ON CONFLICT 也是并发控制：两个消费者同时插入相同消息 ID 时，
	// PostgreSQL 会让其中一个等待，最终只有一个事务得到 rows == 1。
	result, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO inbox_messages (message_id, topic, payload_hash, processed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (message_id) DO NOTHING
	`, messageID, topic, payloadHash, processedAt)
	if err != nil {
		return false, fmt.Errorf("insert inbox message: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read inserted inbox rows: %w", err)
	}
	if rows == 1 {
		return true, nil
	}
	var existingTopic, existingPayloadHash string
	if err := tx.tx.QueryRowContext(tx.ctx, `
		SELECT topic, payload_hash FROM inbox_messages WHERE message_id = $1
	`, messageID).Scan(&existingTopic, &existingPayloadHash); err != nil {
		return false, fmt.Errorf("read duplicate inbox message: %w", err)
	}
	if existingTopic != topic || existingPayloadHash != payloadHash {
		return false, paymentapp.ErrInboxMessageConflict
	}
	return false, nil
}

func (tx *paymentTransaction) SavePayment(created payment.Payment) error {
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO payments (
			id, order_id, customer_id, amount_cents, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, created.ID(), created.OrderID(), created.CustomerID(), created.AmountCents(),
		created.Status(), created.CreatedAt(), created.UpdatedAt())
	if err != nil {
		return fmt.Errorf("insert payment: %w", err)
	}
	return nil
}

func (tx *paymentTransaction) SaveTask(task paymentapp.Task) error {
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO payment_tasks (id, payment_id, idempotency_key, next_attempt_at)
		VALUES ($1, $2, $3, $4)
	`, task.ID, task.Payment.ID(), task.IdempotencyKey, task.Payment.CreatedAt())
	if err != nil {
		return fmt.Errorf("insert payment task: %w", err)
	}
	return nil
}

func (s *PaymentStore) GetPaymentByOrderID(
	ctx context.Context, orderID string,
) (payment.Payment, error) {
	got, err := scanPayment(s.db.QueryRowContext(
		ctx, paymentSelect+` WHERE order_id = $1`, strings.TrimSpace(orderID),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.Payment{}, payment.ErrPaymentNotFound
	}
	if err != nil {
		return payment.Payment{}, fmt.Errorf("get payment by order ID: %w", err)
	}
	return got, nil
}

func (s *PaymentStore) ClaimTasks(
	ctx context.Context, leaseID string, batchSize int, now, lockedUntil time.Time,
) ([]paymentapp.Task, error) {
	if strings.TrimSpace(leaseID) == "" || batchSize <= 0 || !lockedUntil.After(now) {
		return nil, paymentapp.ErrInvalidTaskClaim
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin payment task claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 行锁只覆盖快速认领事务；提交后由 lease_id/locked_until 表示处理权，
	// 避免在等待第三方网络请求时长期占用数据库行锁和连接。
	rows, err := tx.QueryContext(ctx, `
		SELECT
			task.id, task.payment_id, task.idempotency_key, task.attempts,
			payment.order_id, payment.customer_id, payment.amount_cents,
			payment.status, payment.provider_reference, payment.failure_reason,
			payment.created_at, payment.updated_at
		FROM payment_tasks AS task
		JOIN payments AS payment ON payment.id = task.payment_id
		WHERE task.completed_at IS NULL
			AND task.next_attempt_at <= $1
			AND (task.locked_until IS NULL OR task.locked_until <= $1)
			AND payment.status IN ('pending', 'processing', 'unknown')
		ORDER BY task.next_attempt_at, task.id
		LIMIT $2
		FOR UPDATE OF task SKIP LOCKED
	`, now, batchSize)
	if err != nil {
		return nil, fmt.Errorf("select payment tasks: %w", err)
	}
	defer rows.Close()

	tasks := make([]paymentapp.Task, 0, batchSize)
	for rows.Next() {
		var taskID, paymentID, idempotencyKey string
		var attempts int
		var orderID, customerID, status string
		var amountCents int64
		var providerReference, failureReason sql.NullString
		var createdAt, updatedAt time.Time
		if err := rows.Scan(
			&taskID, &paymentID, &idempotencyKey, &attempts, &orderID, &customerID,
			&amountCents, &status, &providerReference, &failureReason, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan payment task: %w", err)
		}
		current, err := payment.Restore(
			paymentID, orderID, customerID, amountCents, payment.Status(status),
			providerReference.String, failureReason.String, createdAt, updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("restore payment %q: %w", paymentID, err)
		}
		processing, err := current.StartProcessing(now)
		if err != nil {
			return nil, fmt.Errorf("start payment %q: %w", paymentID, err)
		}
		tasks = append(tasks, paymentapp.Task{
			ID: taskID, Payment: processing, IdempotencyKey: idempotencyKey,
			Attempts: attempts + 1, LeaseID: leaseID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment tasks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close payment task rows: %w", err)
	}

	for _, task := range tasks {
		if _, err := tx.ExecContext(ctx, `
			UPDATE payments
			SET status = $1, failure_reason = NULL, updated_at = $2
			WHERE id = $3
		`, task.Payment.Status(), task.Payment.UpdatedAt(), task.Payment.ID()); err != nil {
			return nil, fmt.Errorf("mark payment processing: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE payment_tasks
			SET attempts = attempts + 1, lease_id = $1, locked_until = $2
			WHERE id = $3
		`, leaseID, lockedUntil, task.ID); err != nil {
			return nil, fmt.Errorf("lease payment task: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit payment task claim: %w", err)
	}
	return tasks, nil
}

func (s *PaymentStore) CompleteTask(
	ctx context.Context, taskID, leaseID string, outcome paymentapp.TaskOutcome, completedAt time.Time,
) error {
	if outcome.Payment.Status() != payment.StatusSucceeded && outcome.Payment.Status() != payment.StatusFailed {
		return paymentapp.ErrInvalidTask
	}
	return s.updateTask(ctx, taskID, leaseID, outcome, completedAt, true)
}

func (s *PaymentStore) RetryTask(
	ctx context.Context, taskID, leaseID string, outcome paymentapp.TaskOutcome, nextAttemptAt time.Time,
) error {
	if outcome.Payment.Status() != payment.StatusUnknown {
		return paymentapp.ErrInvalidTask
	}
	return s.updateTask(ctx, taskID, leaseID, outcome, nextAttemptAt, false)
}

func (s *PaymentStore) updateTask(
	ctx context.Context, taskID, leaseID string, outcome paymentapp.TaskOutcome, at time.Time, complete bool,
) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin payment task update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var paymentID string
	// 不单独以时间判断租约是否过期。只要 lease_id 尚未被新 Worker 替换，
	// 当前 Worker 仍可提交；一旦重新认领，旧 lease_id 就无法匹配。
	err = tx.QueryRowContext(ctx, `
		SELECT payment_id FROM payment_tasks
		WHERE id = $1 AND lease_id = $2 AND completed_at IS NULL
		FOR UPDATE
	`, taskID, leaseID).Scan(&paymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return paymentapp.ErrTaskLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock payment task: %w", err)
	}
	if paymentID != outcome.Payment.ID() {
		return paymentapp.ErrTaskLeaseLost
	}

	providerReference := nullableText(outcome.Payment.ProviderReference())
	failureReason := nullableText(outcome.Payment.FailureReason())
	result, err := tx.ExecContext(ctx, `
		UPDATE payments SET
			status = $1, provider_reference = $2, failure_reason = $3, updated_at = $4
		WHERE id = $5 AND status = 'processing'
	`, outcome.Payment.Status(), providerReference, failureReason,
		outcome.Payment.UpdatedAt(), outcome.Payment.ID())
	if err != nil {
		return fmt.Errorf("update payment result: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read payment result rows: %w", err)
	}
	if rowsAffected != 1 {
		return paymentapp.ErrTaskLeaseLost
	}

	if complete {
		_, err = tx.ExecContext(ctx, `
			UPDATE payment_tasks SET
				completed_at = $1, lease_id = NULL, locked_until = NULL, last_error = $2
			WHERE id = $3 AND lease_id = $4
		`, at, nullableText(outcome.Reason), taskID, leaseID)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE payment_tasks SET
				next_attempt_at = $1, lease_id = NULL, locked_until = NULL, last_error = $2
			WHERE id = $3 AND lease_id = $4
		`, at, nullableText(outcome.Reason), taskID, leaseID)
	}
	if err != nil {
		return fmt.Errorf("update payment task result: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit payment task update: %w", err)
	}
	return nil
}

const paymentSelect = `
	SELECT id, order_id, customer_id, amount_cents, status,
		provider_reference, failure_reason, created_at, updated_at
	FROM payments
`

type paymentScanner interface{ Scan(...any) error }

func scanPayment(scanner paymentScanner) (payment.Payment, error) {
	var id, orderID, customerID, status string
	var amountCents int64
	var providerReference, failureReason sql.NullString
	var createdAt, updatedAt time.Time
	if err := scanner.Scan(&id, &orderID, &customerID, &amountCents, &status,
		&providerReference, &failureReason, &createdAt, &updatedAt); err != nil {
		return payment.Payment{}, err
	}
	return payment.Restore(id, orderID, customerID, amountCents, payment.Status(status),
		providerReference.String, failureReason.String, createdAt, updatedAt)
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
