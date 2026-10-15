// Package audit records security-sensitive authorization decisions.
package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	OutcomeAllowed = "allowed"
	OutcomeDenied  = "denied"
	DefaultLimit   = 50
	MaxLimit       = 100
)

type Event struct {
	ID           string            `json:"id"`
	ActorID      string            `json:"actor_id"`
	Action       string            `json:"action"`
	ResourceType string            `json:"resource_type"`
	ResourceID   string            `json:"resource_id"`
	Outcome      string            `json:"outcome"`
	RequestID    string            `json:"request_id"`
	Metadata     map[string]string `json:"metadata"`
	OccurredAt   time.Time         `json:"occurred_at"`
}

type RecordCommand struct {
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	RequestID    string
	Metadata     map[string]string
}

type Store interface {
	Append(context.Context, Event) error
	List(context.Context, int) ([]Event, error)
}

type IDGenerator interface {
	NewID() (string, error)
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	store Store
	ids   IDGenerator
	clock Clock
}

func NewService(store Store, ids IDGenerator, clock Clock) *Service {
	return &Service{store: store, ids: ids, clock: clock}
}

func (s *Service) Record(ctx context.Context, command RecordCommand) error {
	if s == nil || s.store == nil || s.ids == nil || s.clock == nil {
		return errors.New("audit service is not configured")
	}
	command.ActorID = strings.TrimSpace(command.ActorID)
	command.Action = strings.TrimSpace(command.Action)
	command.ResourceType = strings.TrimSpace(command.ResourceType)
	command.ResourceID = strings.TrimSpace(command.ResourceID)
	command.RequestID = strings.TrimSpace(command.RequestID)
	if command.ActorID == "" || command.Action == "" || command.ResourceType == "" || command.ResourceID == "" {
		return errors.New("invalid audit event identity")
	}
	if command.Outcome != OutcomeAllowed && command.Outcome != OutcomeDenied {
		return errors.New("invalid audit event outcome")
	}
	eventID, err := s.ids.NewID()
	if err != nil {
		return fmt.Errorf("generate audit event ID: %w", err)
	}
	metadata := make(map[string]string, len(command.Metadata))
	for key, value := range command.Metadata {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key != "" && value != "" {
			metadata[key] = value
		}
	}
	return s.store.Append(ctx, Event{
		ID: eventID, ActorID: command.ActorID, Action: command.Action,
		ResourceType: command.ResourceType, ResourceID: command.ResourceID,
		Outcome: command.Outcome, RequestID: command.RequestID,
		Metadata: metadata, OccurredAt: s.clock.Now().UTC(),
	})
}

func (s *Service) List(ctx context.Context, limit int) ([]Event, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("audit service is not configured")
	}
	if limit == 0 {
		limit = DefaultLimit
	}
	if limit < 1 || limit > MaxLimit {
		return nil, errors.New("audit limit must be between 1 and 100")
	}
	return s.store.List(ctx, limit)
}
