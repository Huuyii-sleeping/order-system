package audit_test

import (
	"context"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/application/audit"
)

type idStub struct{ id string }

func (s idStub) NewID() (string, error) { return s.id, nil }

type clockStub struct{ now time.Time }

func (s clockStub) Now() time.Time { return s.now }

func TestServiceRecordsBoundedAuthorizationFacts(t *testing.T) {
	store := memory.NewAuditStore()
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	service := audit.NewService(store, idStub{"audit-1"}, clockStub{now})
	metadata := map[string]string{"role": "operator"}
	if err := service.Record(context.Background(), audit.RecordCommand{
		ActorID: "user-1", Action: "audit.events.list", ResourceType: "audit_log",
		ResourceID: "audit_events", Outcome: audit.OutcomeAllowed,
		RequestID: "request-1", Metadata: metadata,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	metadata["role"] = "mutated"
	events, err := service.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || events[0].ID != "audit-1" || events[0].Metadata["role"] != "operator" || !events[0].OccurredAt.Equal(now) {
		t.Fatalf("events = %+v", events)
	}
}

func TestServiceRejectsInvalidOutcomeAndUnboundedList(t *testing.T) {
	service := audit.NewService(memory.NewAuditStore(), idStub{"audit-1"}, clockStub{time.Now()})
	if err := service.Record(context.Background(), audit.RecordCommand{
		ActorID: "user-1", Action: "read", ResourceType: "audit", ResourceID: "all", Outcome: "success",
	}); err == nil {
		t.Fatal("Record() accepted an unsupported outcome")
	}
	if _, err := service.List(context.Background(), audit.MaxLimit+1); err == nil {
		t.Fatal("List() accepted an unbounded limit")
	}
}
