package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	authapp "example.com/order-system/internal/application/auth"
	"example.com/order-system/internal/domain/identity"
	"example.com/order-system/internal/transport/httpapi"
)

type authUseCasesStub struct {
	registerResult  identity.User
	registerErr     error
	registerCommand authapp.RegisterCommand
	loginResult     authapp.LoginResult
	loginErr        error
	loginCommand    authapp.LoginCommand
	refreshResult   authapp.RefreshResult
	refreshErr      error
	refreshToken    string
	userID          string
	authErr         error
	authToken       string
	logoutToken     string
	logoutErr       error
	logoutAllUserID string
	logoutAllErr    error
}

func (s *authUseCasesStub) Register(_ context.Context, command authapp.RegisterCommand) (identity.User, error) {
	s.registerCommand = command
	return s.registerResult, s.registerErr
}

func (s *authUseCasesStub) Login(_ context.Context, command authapp.LoginCommand) (authapp.LoginResult, error) {
	s.loginCommand = command
	return s.loginResult, s.loginErr
}

func (s *authUseCasesStub) Refresh(_ context.Context, token string) (authapp.RefreshResult, error) {
	s.refreshToken = token
	return s.refreshResult, s.refreshErr
}

func (s *authUseCasesStub) Authenticate(_ context.Context, token string) (string, error) {
	s.authToken = token
	return s.userID, s.authErr
}

func (s *authUseCasesStub) Logout(_ context.Context, token string) error {
	s.logoutToken = token
	return s.logoutErr
}

func (s *authUseCasesStub) LogoutAll(_ context.Context, userID string) error {
	s.logoutAllUserID = userID
	return s.logoutAllErr
}

func authenticatedAs(userID string) *authUseCasesStub {
	return &authUseCasesStub{userID: userID}
}

func TestRegisterUser(t *testing.T) {
	created := mustHTTPUser(t)
	auth := &authUseCasesStub{registerResult: created}
	request := httptest.NewRequest(
		http.MethodPost,
		"/auth/register",
		bytes.NewBufferString(`{"email":"learner@example.com","password":"correct-horse"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status code = %d, want %d; body = %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if got := response.Header().Get("Location"); got != "/users/user-1" {
		t.Errorf("Location = %q, want /users/user-1", got)
	}
	if auth.registerCommand.Password != "correct-horse" {
		t.Errorf("Register() command = %+v", auth.registerCommand)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("password")) {
		t.Errorf("response exposes password data: %s", response.Body.String())
	}
}

func TestRegisterRejectsOversizedBodyBeforeUseCase(t *testing.T) {
	auth := &authUseCasesStub{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/auth/register",
		strings.NewReader(`{"email":"`+strings.Repeat("x", 1<<20)+`@example.com","password":"correct-horse"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status code = %d, want %d; body = %s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	if auth.registerCommand != (authapp.RegisterCommand{}) {
		t.Fatal("oversized request must not call Register()")
	}
}

func TestLoginReturnsBearerToken(t *testing.T) {
	user := mustHTTPUser(t)
	expiresAt := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	auth := &authUseCasesStub{loginResult: authapp.LoginResult{
		User: user, AccessToken: "raw-session-token", AccessExpiresAt: expiresAt,
		RefreshToken: "raw-refresh-token", RefreshExpiresAt: expiresAt.Add(24 * time.Hour),
	}}
	request := httptest.NewRequest(
		http.MethodPost,
		"/auth/login",
		bytes.NewBufferString(`{"email":"learner@example.com","password":"correct-horse"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d; body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.AccessToken != "raw-session-token" || body.TokenType != "Bearer" {
		t.Errorf("login response = %+v", body)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %+v, want refresh and CSRF cookies", cookies)
	}
	if cookie := cookies[0]; cookie.Name != "refresh_token" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("refresh cookie = %+v, want HttpOnly strict refresh_token", cookie)
	}
	if cookie := cookies[1]; cookie.Name != "csrf_token" || cookie.HttpOnly || cookie.Value == "" || cookie.Path != "/" {
		t.Errorf("CSRF cookie = %+v, want readable non-empty csrf_token", cookie)
	}
}

func TestLoginCanSetSecureAuthCookies(t *testing.T) {
	user := mustHTTPUser(t)
	expiresAt := time.Now().Add(time.Hour)
	auth := &authUseCasesStub{loginResult: authapp.LoginResult{
		User: user, AccessToken: "access", AccessExpiresAt: expiresAt,
		RefreshToken: "refresh", RefreshExpiresAt: expiresAt,
	}}
	request := httptest.NewRequest(
		http.MethodPost,
		"/auth/login",
		bytes.NewBufferString("{\"email\":\"learner@example.com\",\"password\":\"correct-horse\"}"),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewRouterWithAuthSecurity(
		catalogQueriesStub{}, auth, nil,
		httpapi.AuthSecurityConfig{SecureCookies: true},
	).ServeHTTP(response, request)

	for _, cookie := range response.Result().Cookies() {
		if !cookie.Secure {
			t.Errorf("cookie %q Secure = false, want true", cookie.Name)
		}
	}
}

func TestVersionedLoginScopesRefreshCookieToVersionedAuthPath(t *testing.T) {
	user := mustHTTPUser(t)
	expiresAt := time.Now().Add(time.Hour)
	auth := &authUseCasesStub{loginResult: authapp.LoginResult{
		User: user, AccessToken: "access", AccessExpiresAt: expiresAt,
		RefreshToken: "refresh", RefreshExpiresAt: expiresAt,
	}}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/login",
		bytes.NewBufferString(`{"email":"learner@example.com","password":"correct-horse"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	cookies := response.Result().Cookies()
	if response.Code != http.StatusOK || len(cookies) != 2 {
		t.Fatalf("versioned login = status %d cookies %+v", response.Code, cookies)
	}
	if got := cookies[0].Path; got != "/api/v1/auth" {
		t.Fatalf("versioned refresh cookie path = %q", got)
	}
}

func TestRefreshRotatesCookieAndReturnsOnlyAccessToken(t *testing.T) {
	expiresAt := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	auth := &authUseCasesStub{refreshResult: authapp.RefreshResult{
		AccessToken: "new-access", AccessExpiresAt: expiresAt,
		RefreshToken: "new-refresh", RefreshExpiresAt: expiresAt.Add(24 * time.Hour),
	}}
	request := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	request.AddCookie(&http.Cookie{Name: "refresh_token", Value: "old-refresh"})
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: "csrf-value"})
	request.Header.Set(httpapi.CSRFTokenHeader, "csrf-value")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if auth.refreshToken != "old-refresh" {
		t.Errorf("Refresh() token = %q, want old-refresh", auth.refreshToken)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := response.Header().Get("Set-Cookie"); !bytes.Contains([]byte(got), []byte("new-refresh")) {
		t.Errorf("Set-Cookie = %q, want rotated refresh cookie", got)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("new-refresh")) {
		t.Errorf("refresh token leaked in response body: %s", response.Body.String())
	}
}

func TestRefreshRequiresMatchingCSRFToken(t *testing.T) {
	auth := &authUseCasesStub{}
	request := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	request.AddCookie(&http.Cookie{Name: "refresh_token", Value: "refresh-value"})
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusForbidden)
	}
	if auth.refreshToken != "" {
		t.Errorf("Refresh() was called with %q despite failed CSRF validation", auth.refreshToken)
	}
}

func TestRefreshRequiresRefreshCookieAfterCSRFSucceeds(t *testing.T) {
	auth := &authUseCasesStub{}
	request := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	request.AddCookie(&http.Cookie{Name: "csrf_token", Value: "csrf-value"})
	request.Header.Set(httpapi.CSRFTokenHeader, "csrf-value")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestLoginRejectsInvalidCredentialsWithoutBearerChallenge(t *testing.T) {
	auth := &authUseCasesStub{loginErr: authapp.ErrInvalidCredentials}
	request := httptest.NewRequest(
		http.MethodPost,
		"/auth/login",
		bytes.NewBufferString(`{"email":"learner@example.com","password":"wrong-password"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if got := response.Header().Get("WWW-Authenticate"); got != "" {
		t.Errorf("WWW-Authenticate = %q, want empty on credential login", got)
	}
}

func TestProtectedOrderRouteRequiresValidBearerToken(t *testing.T) {
	for _, tt := range []struct {
		name    string
		header  string
		authErr error
	}{
		{name: "missing header"},
		{name: "wrong scheme", header: "Basic abc"},
		{name: "invalid token", header: "Bearer invalid", authErr: authapp.ErrInvalidSession},
	} {
		t.Run(tt.name, func(t *testing.T) {
			auth := &authUseCasesStub{authErr: tt.authErr}
			request := httptest.NewRequest(http.MethodGet, "/orders/order-1", nil)
			request.Header.Set("Authorization", tt.header)
			response := httptest.NewRecorder()

			httpapi.NewRouter(catalogQueriesStub{}, auth, &orderUseCasesStub{}).ServeHTTP(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status code = %d, want %d", response.Code, http.StatusUnauthorized)
			}
			if got := response.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want Bearer", got)
			}
		})
	}
}

func TestProtectedOrderRouteHidesAuthenticationInfrastructureError(t *testing.T) {
	auth := &authUseCasesStub{authErr: errors.New("database unavailable")}
	request := httptest.NewRequest(http.MethodGet, "/orders/order-1", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, &orderUseCasesStub{}).ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}

func TestLogoutRevokesCurrentBearerToken(t *testing.T) {
	auth := authenticatedAs("user-1")
	request := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	request.Header.Set("Authorization", "Bearer session-token")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNoContent)
	}
	if auth.authToken != "session-token" || auth.logoutToken != "session-token" {
		t.Errorf("tokens passed to auth/logout = %q/%q", auth.authToken, auth.logoutToken)
	}
}

func TestLogoutAllUsesAuthenticatedUserFromContext(t *testing.T) {
	auth := authenticatedAs("user-1")
	request := httptest.NewRequest(http.MethodPost, "/auth/logout-all", nil)
	request.Header.Set("Authorization", "Bearer session-token")
	response := httptest.NewRecorder()

	httpapi.NewRouter(catalogQueriesStub{}, auth, nil).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNoContent)
	}
	if auth.logoutAllUserID != "user-1" {
		t.Errorf("LogoutAll() user ID = %q, want user-1", auth.logoutAllUserID)
	}
}

func mustHTTPUser(t *testing.T) identity.User {
	t.Helper()
	user, err := identity.NewUser(
		"user-1",
		"learner@example.com",
		"password-hash",
		time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("create user fixture: %v", err)
	}
	return user
}
