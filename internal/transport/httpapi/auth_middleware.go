package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	authapp "example.com/order-system/internal/application/auth"
)

type authenticatedUserContextKey struct{}

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
			writeError(w, http.StatusInternalServerError, "internal server error")
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

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, "invalid or missing bearer token")
}
