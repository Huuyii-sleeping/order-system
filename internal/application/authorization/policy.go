// Package authorization defines principals, roles and permission policy.
package authorization

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Role string
type Permission string

const (
	RoleCustomer Role = "customer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"

	PermissionAuditRead       Permission = "audit:read"
	PermissionMessageRecovery Permission = "message-recovery:execute"
)

type Principal struct {
	UserID string
	Role   Role
}

func ParseRole(value string) (Role, bool) {
	role := Role(strings.ToLower(strings.TrimSpace(value)))
	switch role {
	case RoleCustomer, RoleOperator, RoleAdmin:
		return role, true
	default:
		return "", false
	}
}

type Policy struct{}

func (Policy) Allowed(role Role, permission Permission) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleOperator:
		return permission == PermissionAuditRead || permission == PermissionMessageRecovery
	default:
		return false
	}
}

var ErrPermissionDenied = errors.New("permission denied")

type RoleStore interface {
	GetUserRole(context.Context, string) (Role, error)
}

type Service struct {
	roles  RoleStore
	policy Policy
}

func NewService(roles RoleStore, policy Policy) *Service {
	return &Service{roles: roles, policy: policy}
}

func (s *Service) Authorize(ctx context.Context, userID string, permission Permission) error {
	if s == nil || s.roles == nil || strings.TrimSpace(userID) == "" {
		return ErrPermissionDenied
	}
	role, err := s.roles.GetUserRole(ctx, strings.TrimSpace(userID))
	if err != nil {
		return fmt.Errorf("get authorization role: %w", err)
	}
	if !s.policy.Allowed(role, permission) {
		return ErrPermissionDenied
	}
	return nil
}
