package httpapi

import (
	"errors"
	"net/http"
	"time"

	authapp "example.com/order-system/internal/application/auth"
	"example.com/order-system/internal/domain/identity"
)

type authHandler struct {
	useCases     AuthUseCases
	secureCookie bool
}

func newAuthHandler(useCases AuthUseCases, secureCookie bool) *authHandler {
	return &authHandler{useCases: useCases, secureCookie: secureCookie}
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

type loginResponse struct {
	AccessToken string       `json:"access_token"`
	TokenType   string       `json:"token_type"`
	ExpiresAt   time.Time    `json:"expires_at"`
	User        userResponse `json:"user"`
}

const refreshCookieName = "refresh_token"

func setRefreshCookie(w http.ResponseWriter, token string, expiresAt time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: token, Path: "/auth",
		Expires: expiresAt, HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func setCSRFCookie(w http.ResponseWriter, token string, expiresAt time.Time, secure bool) {
	http.SetCookie(w, &http.Cookie{
		// CSRF Cookie 必须让根路径页面能够读取，SPA 才能把它复制到 Header。
		// 它不是认证凭证，因此不设置 HttpOnly。
		Name: csrfCookieName, Value: token, Path: "/",
		Expires: expiresAt, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func clearAuthCookies(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: refreshCookieName, Value: "", Path: "/auth", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: "", Path: "/", MaxAge: -1,
		Secure: secure, SameSite: http.SameSiteStrictMode,
	})
}

func (h *authHandler) register(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeCredentials(w, r)
	if !ok {
		return
	}

	created, err := h.useCases.Register(r.Context(), authapp.RegisterCommand{
		Email: request.Email, Password: request.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrInvalidEmail):
			writeError(w, http.StatusBadRequest, "email is invalid")
		case errors.Is(err, identity.ErrInvalidPassword):
			writeError(w, http.StatusBadRequest, "password must be between 8 and 72 bytes")
		case errors.Is(err, identity.ErrEmailAlreadyExists):
			writeError(w, http.StatusConflict, "email already exists")
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	w.Header().Set("Location", "/users/"+created.ID())
	_ = writeJSON(w, http.StatusCreated, toUserResponse(created))
}

func (h *authHandler) login(w http.ResponseWriter, r *http.Request) {
	request, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	csrfToken, err := newCSRFToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	result, err := h.useCases.Login(r.Context(), authapp.LoginCommand{
		Email: request.Email, Password: request.Password,
	})
	if errors.Is(err, authapp.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Access Token 通过响应体返回；Refresh Token 只通过 HttpOnly Cookie 返回，
	// 浏览器脚本无法直接读取长期凭证。
	w.Header().Set("Cache-Control", "no-store")
	setRefreshCookie(w, result.RefreshToken, result.RefreshExpiresAt, h.secureCookie)
	setCSRFCookie(w, csrfToken, result.RefreshExpiresAt, h.secureCookie)
	_ = writeJSON(w, http.StatusOK, loginResponse{
		AccessToken: result.AccessToken,
		TokenType:   "Bearer",
		ExpiresAt:   result.AccessExpiresAt,
		User:        toUserResponse(result.User),
	})
}

func (h *authHandler) refresh(w http.ResponseWriter, r *http.Request) {
	if !validCSRFToken(r) {
		writeError(w, http.StatusForbidden, "invalid or missing CSRF token")
		return
	}
	csrfToken, err := newCSRFToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	cookie, err := r.Cookie(refreshCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "invalid or missing refresh token")
		return
	}

	result, err := h.useCases.Refresh(r.Context(), cookie.Value)
	if errors.Is(err, authapp.ErrInvalidRefresh) {
		clearAuthCookies(w, h.secureCookie)
		writeError(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	setRefreshCookie(w, result.RefreshToken, result.RefreshExpiresAt, h.secureCookie)
	setCSRFCookie(w, csrfToken, result.RefreshExpiresAt, h.secureCookie)
	w.Header().Set("Cache-Control", "no-store")
	response := struct {
		AccessToken string    `json:"access_token"`
		TokenType   string    `json:"token_type"`
		ExpiresAt   time.Time `json:"expires_at"`
	}{
		AccessToken: result.AccessToken,
		TokenType:   "Bearer",
		ExpiresAt:   result.AccessExpiresAt,
	}
	_ = writeJSON(w, http.StatusOK, response)
}

func (h *authHandler) logout(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		writeUnauthorized(w)
		return
	}

	if err := h.useCases.Logout(r.Context(), token); err != nil {
		if errors.Is(err, authapp.ErrInvalidSession) {
			writeUnauthorized(w)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	clearAuthCookies(w, h.secureCookie)
	w.WriteHeader(http.StatusNoContent)
}

func (h *authHandler) logoutAll(w http.ResponseWriter, r *http.Request) {
	userID, ok := AuthenticatedUserID(r.Context())
	if !ok {
		writeUnauthorized(w)
		return
	}

	if err := h.useCases.LogoutAll(r.Context(), userID); err != nil {
		if errors.Is(err, authapp.ErrInvalidSession) {
			writeUnauthorized(w)
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	clearAuthCookies(w, h.secureCookie)
	w.WriteHeader(http.StatusNoContent)
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentialsRequest, bool) {
	if !hasJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return credentialsRequest{}, false
	}

	var request credentialsRequest
	if err := decodeJSONBody(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return credentialsRequest{}, false
	}
	return request, true
}

func toUserResponse(user identity.User) userResponse {
	return userResponse{ID: user.ID(), Email: user.Email(), CreatedAt: user.CreatedAt()}
}
