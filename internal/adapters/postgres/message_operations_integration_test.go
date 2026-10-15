package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/application/messageops"
)

func TestIntegrationMessageOperationsRetriesOutboxWithStateGuard(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO outbox_messages (
			id, topic, message_key, payload, occurred_at, next_attempt_at,
			attempts, last_error
		)
		VALUES ($1, 'orders.created.v1', 'order-1', '{"order_id":"order-1"}',
		        $2::timestamptz, $2::timestamptz + INTERVAL '1 hour', 3, 'broker unavailable')
	`, "recovery-message-1", now); err != nil {
		t.Fatalf("seed failed outbox message: %v", err)
	}

	store := postgres.NewMessageOperationsStore(db)
	if _, err := store.RetryOutbox(ctx, messageops.RetryOutboxCommand{
		ActionID: "stale-action", MessageID: "recovery-message-1",
		ExpectedAttempts: 2, Actor: "operator-1", Reason: "dependency recovered", Now: now,
	}); !errors.Is(err, messageops.ErrOutboxStateChanged) {
		t.Fatalf("stale RetryOutbox() error = %v", err)
	}

	action, err := store.RetryOutbox(ctx, messageops.RetryOutboxCommand{
		ActionID: "recovery-action-1", MessageID: "recovery-message-1",
		ExpectedAttempts: 3, Actor: "operator-1", Reason: "dependency recovered", Now: now,
	})
	if err != nil {
		t.Fatalf("RetryOutbox() error = %v", err)
	}
	if action.Status != messageops.StatusCompleted {
		t.Fatalf("recovery action = %+v", action)
	}

	record, err := store.GetOutbox(ctx, "recovery-message-1")
	if err != nil {
		t.Fatalf("GetOutbox() error = %v", err)
	}
	if !record.NextAttemptAt.Equal(now) || record.Attempts != 3 || !record.HasLastError {
		t.Fatalf("recovered outbox record = %+v", record)
	}
	var actions int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM message_recovery_actions WHERE id = 'recovery-action-1'
	`).Scan(&actions); err != nil {
		t.Fatalf("count recovery actions: %v", err)
	}
	if actions != 1 {
		t.Fatalf("recovery actions = %d, want 1", actions)
	}
}

func TestIntegrationMessageOperationsPreventsDuplicateDLQReplay(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := postgres.NewMessageOperationsStore(openMigratedIdentityTestDB(t, ctx, databaseURL))
	now := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	action := messageops.RecoveryAction{
		ID: "dlq-action-1", Type: messageops.ActionDLQReplay,
		Target: "orders.created.v1.dlq/0/42", Actor: "operator-1",
		Reason: "consumer defect fixed", Status: messageops.StatusRequested, CreatedAt: now,
	}
	if err := store.BeginDLQReplay(ctx, action); err != nil {
		t.Fatalf("BeginDLQReplay() error = %v", err)
	}
	action.ID = "dlq-action-2"
	if err := store.BeginDLQReplay(ctx, action); !errors.Is(err, messageops.ErrReplayAlreadyStarted) {
		t.Fatalf("duplicate BeginDLQReplay() error = %v", err)
	}
	if err := store.FinishDLQReplay(ctx, "dlq-action-1", messageops.StatusCompleted, now.Add(time.Second), ""); err != nil {
		t.Fatalf("FinishDLQReplay() error = %v", err)
	}
}
