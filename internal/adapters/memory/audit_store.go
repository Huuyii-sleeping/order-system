package memory

import (
	"context"
	"sort"
	"sync"

	"example.com/order-system/internal/application/audit"
)

type AuditStore struct {
	mu     sync.RWMutex
	events []audit.Event
}

func NewAuditStore() *AuditStore { return &AuditStore{} }

func (s *AuditStore) Append(ctx context.Context, event audit.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, cloneAuditEvent(event))
	return nil
}

func (s *AuditStore) List(ctx context.Context, limit int) ([]audit.Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make([]audit.Event, len(s.events))
	for index, event := range s.events {
		copied[index] = cloneAuditEvent(event)
	}
	sort.Slice(copied, func(left, right int) bool {
		if copied[left].OccurredAt.Equal(copied[right].OccurredAt) {
			return copied[left].ID > copied[right].ID
		}
		return copied[left].OccurredAt.After(copied[right].OccurredAt)
	})
	if len(copied) > limit {
		copied = copied[:limit]
	}
	return copied, nil
}

func cloneAuditEvent(event audit.Event) audit.Event {
	metadata := make(map[string]string, len(event.Metadata))
	for key, value := range event.Metadata {
		metadata[key] = value
	}
	event.Metadata = metadata
	return event
}
