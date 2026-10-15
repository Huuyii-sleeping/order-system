package postgres_test

import (
	"context"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/application/audit"
	"example.com/order-system/internal/application/authorization"
	"example.com/order-system/internal/domain/identity"
)

func TestIntegrationRolesAndAuditEvents(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	identityStore := postgres.NewIdentityStore(db)
	user, err := identity.NewUser("audit-user-1", "audit-user@example.com", "password-hash", now)
	if err != nil {
		t.Fatalf("NewUser() error = %v", err)
	}
	if err := identityStore.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if role, err := identityStore.GetUserRole(ctx, user.ID()); err != nil || role != authorization.RoleCustomer {
		t.Fatalf("default GetUserRole() = %q, %v", role, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE users SET role = 'operator' WHERE id = $1`, user.ID()); err != nil {
		t.Fatalf("provision operator role: %v", err)
	}
	if role, err := identityStore.GetUserRole(ctx, user.ID()); err != nil || role != authorization.RoleOperator {
		t.Fatalf("operator GetUserRole() = %q, %v", role, err)
	}

	store := postgres.NewAuditStore(db)
	want := audit.Event{
		ID: "audit-event-1", ActorID: user.ID(), Action: "audit.events.list",
		ResourceType: "audit_log", ResourceID: "audit_events", Outcome: audit.OutcomeAllowed,
		RequestID: "request-1", Metadata: map[string]string{"role": "operator"}, OccurredAt: now,
	}
	if err := store.Append(ctx, want); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	events, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || events[0].ID != want.ID || events[0].Metadata["role"] != "operator" {
		t.Fatalf("List() = %+v", events)
	}
	if err := identityStore.SetUserRole(
		ctx, "role-change-event", "bootstrap-operator", user.ID(),
		authorization.RoleAdmin, "initial administrator provisioning", now.Add(time.Minute),
	); err != nil {
		t.Fatalf("SetUserRole() error = %v", err)
	}
	if role, err := identityStore.GetUserRole(ctx, user.ID()); err != nil || role != authorization.RoleAdmin {
		t.Fatalf("admin GetUserRole() = %q, %v", role, err)
	}
	events, err = store.List(ctx, 10)
	if err != nil || len(events) != 2 || events[0].Action != "identity.role.change" ||
		events[0].Metadata["previous_role"] != "operator" {
		t.Fatalf("audited role change events = %+v, %v", events, err)
	}
}
