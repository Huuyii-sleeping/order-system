package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/order-system/internal/application/audit"
	"example.com/order-system/internal/application/authorization"
	"example.com/order-system/internal/transport/httpapi"
)

type principalAuthStub struct {
	principal authorization.Principal
}

func (s principalAuthStub) AuthenticatePrincipal(context.Context, string) (authorization.Principal, error) {
	return s.principal, nil
}

type auditRecorderStub struct {
	commands []audit.RecordCommand
	err      error
}

func (s *auditRecorderStub) Record(_ context.Context, command audit.RecordCommand) error {
	s.commands = append(s.commands, command)
	return s.err
}

func TestWithPermissionDeniesCustomerAndRecordsDecision(t *testing.T) {
	recorder := &auditRecorderStub{}
	called := false
	handler := httpapi.WithPermission(
		principalAuthStub{authorization.Principal{UserID: "customer-1", Role: authorization.RoleCustomer}},
		authorization.Policy{}, recorder, authorization.PermissionAuditRead,
		"audit.events.list", "audit_log", "audit_events",
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }),
	)
	request := httptest.NewRequest(http.MethodGet, "/admin/audit-events", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || called {
		t.Fatalf("status = %d, called = %v", response.Code, called)
	}
	if len(recorder.commands) != 1 || recorder.commands[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("audit commands = %+v", recorder.commands)
	}
}

func TestWithPermissionAllowsOperatorOnlyAfterAuditSucceeds(t *testing.T) {
	for _, tt := range []struct {
		name       string
		recordErr  error
		wantStatus int
		wantCalled bool
	}{
		{name: "audit succeeds", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "audit fails closed", recordErr: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := &auditRecorderStub{err: tt.recordErr}
			called := false
			handler := httpapi.WithPermission(
				principalAuthStub{authorization.Principal{UserID: "operator-1", Role: authorization.RoleOperator}},
				authorization.Policy{}, recorder, authorization.PermissionAuditRead,
				"audit.events.list", "audit_log", "audit_events",
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusNoContent)
				}),
			)
			request := httptest.NewRequest(http.MethodGet, "/admin/audit-events", nil)
			request.Header.Set("Authorization", "Bearer access-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.wantStatus || called != tt.wantCalled {
				t.Fatalf("status = %d, called = %v", response.Code, called)
			}
		})
	}
}
