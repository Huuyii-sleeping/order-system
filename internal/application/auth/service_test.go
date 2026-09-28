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

type idGeneratorStub struct{ id string }

func (s idGeneratorStub) NewID() (string, error) { return s.id, nil }

type tokenGeneratorStub struct{ token string }

func (s tokenGeneratorStub) NewToken() (string, error) { return s.token, nil }

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
	if result.Token != "raw-session-token" {
		t.Errorf("token = %q", result.Token)
	}
	if want := clock.now.Add(time.Hour); !result.ExpiresAt.Equal(want) {
		t.Errorf("expires at = %v, want %v", result.ExpiresAt, want)
	}

	userID, err := service.Authenticate(context.Background(), result.Token)
	if err != nil || userID != result.User.ID() {
		t.Fatalf("Authenticate() = %q, %v", userID, err)
	}
	if _, err := service.Authenticate(context.Background(), "another-token"); !errors.Is(err, authapp.ErrInvalidSession) {
		t.Fatalf("Authenticate(unknown) error = %v", err)
	}

	clock.now = result.ExpiresAt
	if _, err := service.Authenticate(context.Background(), result.Token); !errors.Is(err, authapp.ErrInvalidSession) {
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

func newAuthService(store authapp.Store, hasher authapp.PasswordHasher) (*authapp.Service, *clockStub) {
	clock := &clockStub{now: time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)}
	return authapp.NewService(
		store, hasher, idGeneratorStub{id: "user-1"},
		tokenGeneratorStub{token: "raw-session-token"}, clock, time.Hour,
	), clock
}
