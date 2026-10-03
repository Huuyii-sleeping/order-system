package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"example.com/order-system/internal/application/outbox"
)

type OutboxStore struct {
	db *sql.DB
}

func NewOutboxStore(db *sql.DB) *OutboxStore {
	return &OutboxStore{db: db}
}

func (s *OutboxStore) Claim(
	ctx context.Context, leaseID string, batchSize int, now, lockedUntil time.Time,
) ([]outbox.Message, error) {
	if strings.TrimSpace(leaseID) == "" || batchSize <= 0 || !lockedUntil.After(now) {
		return nil, outbox.ErrInvalidClaim
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("begin outbox claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id
			FROM outbox_messages
			WHERE published_at IS NULL
			  AND next_attempt_at <= $1
			  AND (locked_until IS NULL OR locked_until <= $1)
			ORDER BY occurred_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE outbox_messages AS message
		SET lease_id = $3, locked_until = $4, attempts = message.attempts + 1
		FROM candidates
		WHERE message.id = candidates.id
		RETURNING message.id, message.topic, message.message_key, message.payload,
		          message.occurred_at, message.attempts, message.lease_id,
		          message.trace_parent, message.trace_state
	`, now, batchSize, leaseID, lockedUntil)
	if err != nil {
		return nil, fmt.Errorf("claim outbox messages: %w", err)
	}
	defer rows.Close()

	messages := make([]outbox.Message, 0, batchSize)
	for rows.Next() {
		var message outbox.Message
		var traceParent, traceState sql.NullString
		if err := rows.Scan(
			&message.ID, &message.Topic, &message.Key, &message.Payload,
			&message.OccurredAt, &message.Attempts, &message.LeaseID,
			&traceParent, &traceState,
		); err != nil {
			return nil, fmt.Errorf("scan outbox message: %w", err)
		}
		message.TraceContext.TraceParent = traceParent.String
		message.TraceContext.TraceState = traceState.String
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox messages: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbox claim: %w", err)
	}
	return messages, nil
}

func (s *OutboxStore) MarkPublished(
	ctx context.Context, messageID, leaseID string, publishedAt time.Time,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_messages
		SET published_at = $3, lease_id = NULL, locked_until = NULL, last_error = NULL
		WHERE id = $1 AND lease_id = $2 AND published_at IS NULL
	`, messageID, leaseID, publishedAt)
	return classifyOutboxUpdate(result, err, "mark outbox message published")
}

func (s *OutboxStore) MarkFailed(
	ctx context.Context, messageID, leaseID string, nextAttemptAt time.Time, reason string,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_messages
		SET next_attempt_at = $3, last_error = $4, lease_id = NULL, locked_until = NULL
		WHERE id = $1 AND lease_id = $2 AND published_at IS NULL
	`, messageID, leaseID, nextAttemptAt, reason)
	return classifyOutboxUpdate(result, err, "mark outbox message failed")
}

func classifyOutboxUpdate(result sql.Result, err error, operation string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows: %w", operation, err)
	}
	if rowsAffected != 1 {
		return outbox.ErrLeaseLost
	}
	return nil
}
