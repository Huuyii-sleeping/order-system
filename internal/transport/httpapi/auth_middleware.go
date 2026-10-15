package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"example.com/order-system/internal/application/audit"
	authapp "example.com/order-system/internal/application/auth"
	"example.com/order-system/internal/application/authorization"
)

type authenticatedUserContextKey struct{}
type authenticatedPrincipalContextKey struct{}

// WithAuthentication 在进入业务 Handler 前验证 Bearer Token，
// 并把可信用户 ID 放入请求 Context。Handler 不再相信客户端提交 customer_id。
func WithAuthentication(authenticator SessionAuthenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeUnauthorized(w)
			return
		}

		userID, err := authenticator.Authenticate(r.Context(), token)
		if errors.Is(err, authapp.ErrInvalidSession) {
			writeUnauthorized(w)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
			return
		}

		ctx := context.WithValue(r.Context(), authenticatedUserContextKey{}, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func AuthenticatedUserID(ctx context.Context) (string, bool) {
	userID, ok := ctx.Value(authenticatedUserContextKey{}).(string)
	return userID, ok && userID != ""
}

type PrincipalAuthenticator interface {
	AuthenticatePrincipal(context.Context, string) (authorization.Principal, error)
}

type PermissionAuthorizer interface {
	Allowed(authorization.Role, authorization.Permission) bool
}

type AuditRecorder interface {
	Record(context.Context, audit.RecordCommand) error
}

// WithPermission 在敏感 Handler 执行前完成认证、授权和持久化审计。
// 如果审计记录无法写入，则拒绝执行操作，避免出现不可追溯的管理行为。
func WithPermission(
	authenticator PrincipalAuthenticator,
	authorizer PermissionAuthorizer,
	recorder AuditRecorder,
	permission authorization.Permission,
	action, resourceType, resourceID string,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeUnauthorized(w)
			return
		}
		principal, err := authenticator.AuthenticatePrincipal(r.Context(), token)
		if errors.Is(err, authapp.ErrInvalidSession) {
			writeUnauthorized(w)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
			return
		}
		outcome := audit.OutcomeDenied
		if authorizer != nil && authorizer.Allowed(principal.Role, permission) {
			outcome = audit.OutcomeAllowed
		}
		if recorder == nil || recorder.Record(r.Context(), audit.RecordCommand{
			ActorID: principal.UserID, Action: action, ResourceType: resourceType,
			ResourceID: resourceID, Outcome: outcome, RequestID: RequestID(r.Context()),
			Metadata: map[string]string{"role": string(principal.Role), "permission": string(permission)},
		}) != nil {
			writeError(w, http.StatusInternalServerError, errorCodeInternalError, "internal server error")
			return
		}
		if outcome == audit.OutcomeDenied {
			writeError(w, http.StatusForbidden, errorCodePermissionDenied, "permission denied")
			return
		}
		ctx := context.WithValue(r.Context(), authenticatedUserContextKey{}, principal.UserID)
		ctx = context.WithValue(ctx, authenticatedPrincipalContextKey{}, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func AuthenticatedPrincipal(ctx context.Context) (authorization.Principal, bool) {
	principal, ok := ctx.Value(authenticatedPrincipalContextKey{}).(authorization.Principal)
	return principal, ok && principal.UserID != ""
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, errorCodeInvalidBearerToken, "invalid or missing bearer token")
}
