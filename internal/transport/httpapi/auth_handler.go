package httpapi

import (
	"errors"
	"net/http"
	"time"

	authapp "example.com/order-system/internal/application/auth"
	"example.com/order-system/internal/domain/identity"
)

type authHandler struct {
	useCases AuthUseCases
}

func newAuthHandler(useCases AuthUseCases) *authHandler {
	return &authHandler{useCases: useCases}
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

	// 登录响应包含原始会话 Token，禁止代理和浏览器把它缓存下来。
	w.Header().Set("Cache-Control", "no-store")
	_ = writeJSON(w, http.StatusOK, loginResponse{
		AccessToken: result.Token,
		TokenType:   "Bearer",
		ExpiresAt:   result.ExpiresAt,
		User:        toUserResponse(result.User),
	})
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
