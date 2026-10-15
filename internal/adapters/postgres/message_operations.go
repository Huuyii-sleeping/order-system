package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"example.com/order-system/internal/application/messageops"
)

type MessageOperationsStore struct {
	db *sql.DB
}

func NewMessageOperationsStore(db *sql.DB) *MessageOperationsStore {
	return &MessageOperationsStore{db: db}
}

func (s *MessageOperationsStore) GetOutbox(
	ctx context.Context, messageID string,
) (messageops.OutboxRecord, error) {
	var record messageops.OutboxRecord
	var lockedUntil, publishedAt sql.NullTime
	var lastError sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, topic, message_key, attempts, next_attempt_at,
		       locked_until, published_at, last_error
		FROM outbox_messages
		WHERE id = $1
	`, strings.TrimSpace(messageID)).Scan(
		&record.ID, &record.Topic, &record.MessageKey, &record.Attempts,
		&record.NextAttemptAt, &lockedUntil, &publishedAt, &lastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return messageops.OutboxRecord{}, messageops.ErrOutboxNotFound
	}
	if err != nil {
		return messageops.OutboxRecord{}, fmt.Errorf("inspect outbox message: %w", err)
	}
	if lockedUntil.Valid {
		record.LockedUntil = &lockedUntil.Time
	}
	if publishedAt.Valid {
		record.PublishedAt = &publishedAt.Time
	}
	record.HasLastError = lastError.Valid && strings.TrimSpace(lastError.String) != ""
	return record, nil
}

func (s *MessageOperationsStore) RetryOutbox(
	ctx context.Context, command messageops.RetryOutboxCommand,
) (messageops.RecoveryAction, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return messageops.RecoveryAction{}, fmt.Errorf("begin outbox recovery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var attempts int
	var lockedUntil, publishedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT attempts, locked_until, published_at
		FROM outbox_messages
		WHERE id = $1
		FOR UPDATE
	`, command.MessageID).Scan(&attempts, &lockedUntil, &publishedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return messageops.RecoveryAction{}, messageops.ErrOutboxNotFound
	case err != nil:
		return messageops.RecoveryAction{}, fmt.Errorf("lock outbox message: %w", err)
	case publishedAt.Valid:
		return messageops.RecoveryAction{}, messageops.ErrOutboxPublished
	case attempts != command.ExpectedAttempts:
		return messageops.RecoveryAction{}, messageops.ErrOutboxStateChanged
	case lockedUntil.Valid && lockedUntil.Time.After(command.Now):
		return messageops.RecoveryAction{}, messageops.ErrOutboxLeaseActive
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE outbox_messages
		SET next_attempt_at = $2, lease_id = NULL, locked_until = NULL
		WHERE id = $1
	`, command.MessageID, command.Now); err != nil {
		return messageops.RecoveryAction{}, fmt.Errorf("schedule outbox retry: %w", err)
	}
	action := messageops.RecoveryAction{
		ID: command.ActionID, Type: messageops.ActionOutboxRetry, Target: command.MessageID,
		Actor: command.Actor, Reason: command.Reason,
		Status: messageops.StatusCompleted, CreatedAt: command.Now,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO message_recovery_actions (
			id, action_type, target, actor, reason, status, created_at, completed_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
	`, action.ID, action.Type, action.Target, action.Actor, action.Reason, action.Status, action.CreatedAt); err != nil {
		return messageops.RecoveryAction{}, fmt.Errorf("record outbox recovery: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return messageops.RecoveryAction{}, fmt.Errorf("commit outbox recovery: %w", err)
	}
	return action, nil
}

func (s *MessageOperationsStore) GetInbox(
	ctx context.Context, messageID string,
) (messageops.InboxRecord, error) {
	var record messageops.InboxRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT message_id, topic, payload_hash, processed_at
		FROM inbox_messages
		WHERE message_id = $1
	`, strings.TrimSpace(messageID)).Scan(
		&record.MessageID, &record.Topic, &record.PayloadHash, &record.ProcessedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return messageops.InboxRecord{}, messageops.ErrInboxNotFound
	}
	if err != nil {
		return messageops.InboxRecord{}, fmt.Errorf("inspect inbox message: %w", err)
	}
	return record, nil
}

func (s *MessageOperationsStore) BeginDLQReplay(
	ctx context.Context, action messageops.RecoveryAction,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO message_recovery_actions (
			id, action_type, target, actor, reason, status, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, action.ID, action.Type, action.Target, action.Actor, action.Reason, action.Status, action.CreatedAt)
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" &&
		postgresError.ConstraintName == "message_recovery_dlq_target_unique" {
		return messageops.ErrReplayAlreadyStarted
	}
	return fmt.Errorf("begin dead-letter replay: %w", err)
}

func (s *MessageOperationsStore) FinishDLQReplay(
	ctx context.Context, actionID, status string, completedAt time.Time, lastError string,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE message_recovery_actions
		SET status = $2, completed_at = $3, last_error = NULLIF($4, '')
		WHERE id = $1 AND action_type = 'dlq_replay' AND status = 'requested'
	`, actionID, status, completedAt, truncateOperationError(lastError))
	if err != nil {
		return fmt.Errorf("finish dead-letter replay: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read finished dead-letter replay rows: %w", err)
	}
	if rows != 1 {
		return messageops.ErrReplayAlreadyStarted
	}
	return nil
}

func truncateOperationError(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 1000 {
		return string(runes[:1000])
	}
	return string(runes)
}
