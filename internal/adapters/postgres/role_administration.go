package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/order-system/internal/application/authorization"
	"example.com/order-system/internal/domain/identity"
)

// SetUserRole changes a provisioned role and writes its audit event in the same
// transaction. It is intentionally exposed only to the database-backed access
// operations command, not to public registration input.
func (s *IdentityStore) SetUserRole(
	ctx context.Context,
	eventID, actorID, targetUserID string,
	role authorization.Role,
	reason string,
	at time.Time,
) error {
	if _, ok := authorization.ParseRole(string(role)); !ok ||
		strings.TrimSpace(eventID) == "" || strings.TrimSpace(actorID) == "" ||
		strings.TrimSpace(targetUserID) == "" || strings.TrimSpace(reason) == "" {
		return errors.New("invalid role administration request")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin role administration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var previous string
	err = tx.QueryRowContext(ctx, `
		SELECT role FROM users WHERE id = $1 FOR UPDATE
	`, targetUserID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrUserNotFound
	}
	if err != nil {
		return fmt.Errorf("lock role target: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = $2 WHERE id = $1`, targetUserID, role); err != nil {
		return fmt.Errorf("update user role: %w", err)
	}
	metadata, err := json.Marshal(map[string]string{
		"previous_role": previous, "new_role": string(role), "reason": strings.TrimSpace(reason),
	})
	if err != nil {
		return fmt.Errorf("encode role audit metadata: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_events (
			id, actor_id, action, resource_type, resource_id,
			outcome, metadata, occurred_at
		)
		VALUES ($1, $2, 'identity.role.change', 'user', $3, 'allowed', $4, $5)
	`, eventID, actorID, targetUserID, metadata, at); err != nil {
		return fmt.Errorf("record role change audit event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit role administration: %w", err)
	}
	return nil
}
