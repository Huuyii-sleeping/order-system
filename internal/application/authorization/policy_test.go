package authorization_test

import (
	"context"
	"errors"
	"testing"

	"example.com/order-system/internal/application/authorization"
)

func TestPolicyUsesLeastPrivilegeRoleMatrix(t *testing.T) {
	policy := authorization.Policy{}
	for _, tt := range []struct {
		role       authorization.Role
		permission authorization.Permission
		want       bool
	}{
		{authorization.RoleCustomer, authorization.PermissionAuditRead, false},
		{authorization.RoleOperator, authorization.PermissionAuditRead, true},
		{authorization.RoleOperator, authorization.PermissionMessageRecovery, true},
		{authorization.RoleAdmin, authorization.PermissionAuditRead, true},
		{authorization.RoleAdmin, authorization.Permission("future:permission"), true},
	} {
		if got := policy.Allowed(tt.role, tt.permission); got != tt.want {
			t.Errorf("Allowed(%q, %q) = %v, want %v", tt.role, tt.permission, got, tt.want)
		}
	}
}

type roleStoreStub struct{ role authorization.Role }

func (s roleStoreStub) GetUserRole(context.Context, string) (authorization.Role, error) {
	return s.role, nil
}

func TestServiceAuthorizesPersistedRole(t *testing.T) {
	operator := authorization.NewService(roleStoreStub{authorization.RoleOperator}, authorization.Policy{})
	if err := operator.Authorize(context.Background(), "operator-1", authorization.PermissionAuditRead); err != nil {
		t.Fatalf("Authorize(operator) error = %v", err)
	}
	customer := authorization.NewService(roleStoreStub{authorization.RoleCustomer}, authorization.Policy{})
	if err := customer.Authorize(context.Background(), "customer-1", authorization.PermissionAuditRead); !errors.Is(err, authorization.ErrPermissionDenied) {
		t.Fatalf("Authorize(customer) error = %v", err)
	}
}

func TestParseRoleRejectsUnknownValues(t *testing.T) {
	if role, ok := authorization.ParseRole(" Operator "); !ok || role != authorization.RoleOperator {
		t.Fatalf("ParseRole(operator) = %q, %v", role, ok)
	}
	if _, ok := authorization.ParseRole("superuser"); ok {
		t.Fatal("ParseRole(superuser) accepted an unknown role")
	}
}
