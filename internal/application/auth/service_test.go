package authapp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/memory"
	authapp "example.com/order-system/internal/application/auth"
	"example.com/order-system/internal/domain/identity"
)

type passwordHasherStub struct {
	hash      string
	hashInput string
	matches   bool
}

func (s *passwordHasherStub) Hash(password string) (string, error) {
	s.hashInput = password
	return s.hash, nil
}

func (s *passwordHasherStub) Matches(hash, password string) bool {
	return s.matches && hash == s.hash && password == "correct-password"
}

type idGeneratorStub struct {
	id   string
	ids  []string
	next int
}

func (s *idGeneratorStub) NewID() (string, error) {
	if len(s.ids) > 0 {
		id := s.ids[s.next]
		s.next++
		return id, nil
	}
	return s.id, nil
}

type tokenGeneratorStub struct {
	token  string
	tokens []string
	next   int
}

func (s *tokenGeneratorStub) NewToken() (string, error) {
	if len(s.tokens) > 0 {
		token := s.tokens[s.next]
		s.next++
		return token, nil
	}
	return s.token, nil
}

type clockStub struct{ now time.Time }

func (s *clockStub) Now() time.Time { return s.now }

func TestRegisterNormalizesEmailAndStoresOnlyPasswordHash(t *testing.T) {
	store := memory.NewIdentityStore()
	hasher := &passwordHasherStub{hash: "stored-password-hash"}
	service, _ := newAuthService(store, hasher)

	created, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "  Learner@Example.COM ", Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if created.Email() != "learner@example.com" {
		t.Errorf("email = %q, want normalized email", created.Email())
	}
	if created.PasswordHash() != "stored-password-hash" || created.PasswordHash() == hasher.hashInput {
		t.Errorf("password hash = %q", created.PasswordHash())
	}

	stored, err := store.GetUserByEmail(context.Background(), "learner@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail() error = %v", err)
	}
	if stored.PasswordHash() != "stored-password-hash" {
		t.Errorf("stored hash = %q", stored.PasswordHash())
	}
}

func TestRegisterRejectsInvalidInputAndDuplicateEmail(t *testing.T) {
	store := memory.NewIdentityStore()
	service, _ := newAuthService(store, &passwordHasherStub{hash: "hash"})

	if _, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "invalid", Password: "correct-password",
	}); !errors.Is(err, identity.ErrInvalidEmail) {
		t.Fatalf("invalid email error = %v", err)
	}
	if _, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "learner@example.com", Password: "short",
	}); !errors.Is(err, identity.ErrInvalidPassword) {
		t.Fatalf("short password error = %v", err)
	}

	command := authapp.RegisterCommand{Email: "learner@example.com", Password: "correct-password"}
	if _, err := service.Register(context.Background(), command); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	if _, err := service.Register(context.Background(), command); !errors.Is(err, identity.ErrEmailAlreadyExists) {
		t.Fatalf("duplicate Register() error = %v", err)
	}
}

func TestLoginCreatesHashedExpiringSessionAndAuthenticatesToken(t *testing.T) {
	store := memory.NewIdentityStore()
	hasher := &passwordHasherStub{hash: "stored-password-hash", matches: true}
	service, clock := newAuthService(store, hasher)
	if _, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "learner@example.com", Password: "correct-password",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	result, err := service.Login(context.Background(), authapp.LoginCommand{
		Email: "learner@example.com", Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.AccessToken != "raw-access-token" {
		t.Errorf("access token = %q", result.AccessToken)
	}
	if want := clock.now.Add(time.Hour); !result.AccessExpiresAt.Equal(want) {
		t.Errorf("access expires at = %v, want %v", result.AccessExpiresAt, want)
	}
	if result.RefreshToken != "raw-refresh-token" {
		t.Errorf("refresh token = %q", result.RefreshToken)
	}

	userID, err := service.Authenticate(context.Background(), result.AccessToken)
	if err != nil || userID != result.User.ID() {
		t.Fatalf("Authenticate() = %q, %v", userID, err)
	}
	if _, err := service.Authenticate(context.Background(), "another-token"); !errors.Is(err, authapp.ErrInvalidSession) {
		t.Fatalf("Authenticate(unknown) error = %v", err)
	}

	clock.now = result.AccessExpiresAt
	if _, err := service.Authenticate(context.Background(), result.AccessToken); !errors.Is(err, authapp.ErrInvalidSession) {
		t.Fatalf("Authenticate(expired) error = %v", err)
	}
}

func TestLoginUsesOnePublicErrorForUnknownUserAndWrongPassword(t *testing.T) {
	store := memory.NewIdentityStore()
	hasher := &passwordHasherStub{hash: "stored-password-hash"}
	service, _ := newAuthService(store, hasher)

	if _, err := service.Login(context.Background(), authapp.LoginCommand{
		Email: "missing@example.com", Password: "correct-password",
	}); !errors.Is(err, authapp.ErrInvalidCredentials) {
		t.Fatalf("unknown user error = %v", err)
	}

	hasher.matches = true
	if _, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "learner@example.com", Password: "correct-password",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := service.Login(context.Background(), authapp.LoginCommand{
		Email: "learner@example.com", Password: "wrong-password",
	}); !errors.Is(err, authapp.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", err)
	}
}

func TestRefreshRotatesTokenAndRejectsReplay(t *testing.T) {
	store := memory.NewIdentityStore()
	hasher := &passwordHasherStub{hash: "stored-password-hash", matches: true}
	clock := &clockStub{now: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)}
	ids := &idGeneratorStub{ids: []string{"user-1", "family-1"}}
	tokens := &tokenGeneratorStub{tokens: []string{
		"access-1", "refresh-1", "access-2", "refresh-2", "access-3", "refresh-3",
	}}
	service := authapp.NewService(store, hasher, ids, tokens, clock, time.Hour, 24*time.Hour)
	if _, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "learner@example.com", Password: "correct-password",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	login, err := service.Login(context.Background(), authapp.LoginCommand{
		Email: "learner@example.com", Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	clock.now = clock.now.Add(time.Minute)
	refreshed, err := service.Refresh(context.Background(), login.RefreshToken)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if refreshed.AccessToken != "access-2" || refreshed.RefreshToken != "refresh-2" {
		t.Fatalf("refreshed credentials = %+v", refreshed)
	}
	if _, err := service.Authenticate(context.Background(), login.AccessToken); err != nil {
		t.Fatalf("old access token error = %v, want still valid until expiry", err)
	}
	if _, err := service.Refresh(context.Background(), login.RefreshToken); !errors.Is(err, authapp.ErrInvalidRefresh) {
		t.Fatalf("replayed refresh error = %v, want invalid", err)
	}
	if _, err := service.Authenticate(context.Background(), refreshed.AccessToken); !errors.Is(err, authapp.ErrInvalidSession) {
		t.Fatalf("rotated access token after replay error = %v, want invalid", err)
	}
	if _, err := service.Refresh(context.Background(), refreshed.RefreshToken); !errors.Is(err, authapp.ErrInvalidRefresh) {
		t.Fatalf("replacement refresh after replay error = %v, want invalid", err)
	}
}

func TestLogoutRevokesOnlyTheSelectedSession(t *testing.T) {
	store := memory.NewIdentityStore()
	hasher := &passwordHasherStub{hash: "stored-password-hash", matches: true}
	clock := &clockStub{now: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)}
	tokens := &tokenGeneratorStub{tokens: []string{
		"first-access-token", "first-refresh-token",
		"second-access-token", "second-refresh-token",
	}}
	ids := &idGeneratorStub{ids: []string{"user-1", "family-1", "family-2"}}
	service := authapp.NewService(
		store, hasher, ids, tokens, clock, time.Hour, 24*time.Hour,
	)
	if _, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "learner@example.com", Password: "correct-password",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	first, err := service.Login(context.Background(), authapp.LoginCommand{
		Email: "learner@example.com", Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
	clock.now = clock.now.Add(time.Minute)
	second, err := service.Login(context.Background(), authapp.LoginCommand{
		Email: "learner@example.com", Password: "correct-password",
	})
	if err != nil {
		t.Fatalf("second Login() error = %v", err)
	}

	if err := service.Logout(context.Background(), first.AccessToken); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), first.AccessToken); !errors.Is(err, authapp.ErrInvalidSession) {
		t.Fatalf("revoked Authenticate() error = %v", err)
	}
	if _, err := service.Authenticate(context.Background(), second.AccessToken); err != nil {
		t.Fatalf("other session Authenticate() error = %v", err)
	}
	if err := service.Logout(context.Background(), first.AccessToken); err != nil {
		t.Fatalf("repeated Logout() error = %v", err)
	}
}

func TestLogoutAllRevokesEverySessionForTheUser(t *testing.T) {
	store := memory.NewIdentityStore()
	hasher := &passwordHasherStub{hash: "stored-password-hash", matches: true}
	clock := &clockStub{now: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)}
	tokens := &tokenGeneratorStub{tokens: []string{
		"first-access-token", "first-refresh-token",
		"second-access-token", "second-refresh-token",
	}}
	ids := &idGeneratorStub{ids: []string{"user-1", "family-1", "family-2"}}
	service := authapp.NewService(
		store, hasher, ids, tokens, clock, time.Hour, 24*time.Hour,
	)
	if _, err := service.Register(context.Background(), authapp.RegisterCommand{
		Email: "learner@example.com", Password: "correct-password",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	first, err := service.Login(context.Background(), authapp.LoginCommand{Email: "learner@example.com", Password: "correct-password"})
	if err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
	clock.now = clock.now.Add(time.Minute)
	second, err := service.Login(context.Background(), authapp.LoginCommand{Email: "learner@example.com", Password: "correct-password"})
	if err != nil {
		t.Fatalf("second Login() error = %v", err)
	}

	if err := service.LogoutAll(context.Background(), first.User.ID()); err != nil {
		t.Fatalf("LogoutAll() error = %v", err)
	}
	for _, token := range []string{first.AccessToken, second.AccessToken} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, authapp.ErrInvalidSession) {
			t.Fatalf("Authenticate() after LogoutAll() error = %v", err)
		}
	}
}

func newAuthService(store authapp.Store, hasher authapp.PasswordHasher) (*authapp.Service, *clockStub) {
	clock := &clockStub{now: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)}
	return authapp.NewService(
		store, hasher, &idGeneratorStub{id: "user-1"},
		&tokenGeneratorStub{tokens: []string{"raw-access-token", "raw-refresh-token"}}, clock, time.Hour, 24*time.Hour,
	), clock
}
