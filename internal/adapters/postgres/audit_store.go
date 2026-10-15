package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"example.com/order-system/internal/application/audit"
)

type AuditStore struct {
	db *sql.DB
}

func NewAuditStore(db *sql.DB) *AuditStore { return &AuditStore{db: db} }

func (s *AuditStore) Append(ctx context.Context, event audit.Event) error {
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO audit_events (
			id, actor_id, action, resource_type, resource_id,
			outcome, request_id, metadata, occurred_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8, $9)
	`, event.ID, event.ActorID, event.Action, event.ResourceType, event.ResourceID,
		event.Outcome, event.RequestID, metadata, event.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func (s *AuditStore) List(ctx context.Context, limit int) ([]audit.Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, actor_id, action, resource_type, resource_id,
		       outcome, COALESCE(request_id, ''), metadata, occurred_at
		FROM audit_events
		ORDER BY occurred_at DESC, id DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	events := make([]audit.Event, 0, limit)
	for rows.Next() {
		var event audit.Event
		var metadata []byte
		if err := rows.Scan(
			&event.ID, &event.ActorID, &event.Action, &event.ResourceType,
			&event.ResourceID, &event.Outcome, &event.RequestID, &metadata, &event.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		if err := json.Unmarshal(metadata, &event.Metadata); err != nil {
			return nil, fmt.Errorf("decode audit metadata: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}
