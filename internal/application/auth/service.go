// Package authapp 编排注册、登录和会话认证用例。
package authapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/order-system/internal/domain/identity"
)

const (
	MinPasswordBytes = 8
	MaxPasswordBytes = 72
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidSession     = errors.New("invalid or expired session")
)

type Store interface {
	CreateUser(context.Context, identity.User) error
	GetUserByEmail(context.Context, string) (identity.User, error)
	CreateSession(context.Context, identity.Session) error
	GetSessionByTokenHash(context.Context, string, time.Time) (identity.Session, error)
}

type PasswordHasher interface {
	Hash(string) (string, error)
	Matches(string, string) bool
}

type IDGenerator interface {
	NewID() (string, error)
}

type TokenGenerator interface {
	NewToken() (string, error)
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	store      Store
	passwords  PasswordHasher
	ids        IDGenerator
	tokens     TokenGenerator
	clock      Clock
	sessionTTL time.Duration
}

func NewService(
	store Store,
	passwords PasswordHasher,
	ids IDGenerator,
	tokens TokenGenerator,
	clock Clock,
	sessionTTL time.Duration,
) *Service {
	return &Service{
		store: store, passwords: passwords, ids: ids, tokens: tokens,
		clock: clock, sessionTTL: sessionTTL,
	}
}

type RegisterCommand struct {
	Email    string
	Password string
}

func (s *Service) Register(ctx context.Context, command RegisterCommand) (identity.User, error) {
	email, err := identity.NormalizeEmail(command.Email)
	if err != nil {
		return identity.User{}, err
	}
	if err := validatePassword(command.Password); err != nil {
		return identity.User{}, err
	}

	passwordHash, err := s.passwords.Hash(command.Password)
	if err != nil {
		return identity.User{}, fmt.Errorf("hash password: %w", err)
	}
	userID, err := s.ids.NewID()
	if err != nil {
		return identity.User{}, fmt.Errorf("generate user ID: %w", err)
	}

	created, err := identity.NewUser(userID, email, passwordHash, s.clock.Now())
	if err != nil {
		return identity.User{}, fmt.Errorf("create user: %w", err)
	}
	if err := s.store.CreateUser(ctx, created); err != nil {
		return identity.User{}, fmt.Errorf("save user: %w", err)
	}

	return created, nil
}

type LoginCommand struct {
	Email    string
	Password string
}

type LoginResult struct {
	User      identity.User
	Token     string
	ExpiresAt time.Time
}

func (s *Service) Login(ctx context.Context, command LoginCommand) (LoginResult, error) {
	email, err := identity.NormalizeEmail(command.Email)
	if err != nil || command.Password == "" || len(command.Password) > MaxPasswordBytes {
		return LoginResult{}, ErrInvalidCredentials
	}

	user, err := s.store.GetUserByEmail(ctx, email)
	if errors.Is(err, identity.ErrUserNotFound) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("get user by email: %w", err)
	}
	if !s.passwords.Matches(user.PasswordHash(), command.Password) {
		return LoginResult{}, ErrInvalidCredentials
	}

	token, err := s.tokens.NewToken()
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate session token: %w", err)
	}
	createdAt := s.clock.Now()
	expiresAt := createdAt.Add(s.sessionTTL)
	session, err := identity.NewSession(hashToken(token), user.ID(), createdAt, expiresAt)
	if err != nil {
		return LoginResult{}, fmt.Errorf("create session: %w", err)
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return LoginResult{}, fmt.Errorf("save session: %w", err)
	}

	return LoginResult{User: user, Token: token, ExpiresAt: expiresAt}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrInvalidSession
	}

	session, err := s.store.GetSessionByTokenHash(ctx, hashToken(token), s.clock.Now())
	if errors.Is(err, identity.ErrSessionNotFound) {
		return "", ErrInvalidSession
	}
	if err != nil {
		return "", fmt.Errorf("get session: %w", err)
	}

	return session.UserID(), nil
}

func validatePassword(password string) error {
	if len(password) < MinPasswordBytes || len(password) > MaxPasswordBytes {
		return identity.ErrInvalidPassword
	}
	return nil
}

func hashToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
