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

	"example.com/order-system/internal/application/authorization"
	"example.com/order-system/internal/domain/identity"
)

const (
	MinPasswordBytes = 8
	MaxPasswordBytes = 72
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidSession     = errors.New("invalid or expired session")
	ErrInvalidRefresh     = errors.New("invalid or expired refresh token")
)

type Store interface {
	CreateUser(context.Context, identity.User) error
	GetUserByEmail(context.Context, string) (identity.User, error)
	GetUserRole(context.Context, string) (authorization.Role, error)
	CreateSession(context.Context, identity.Session) error
	GetSessionByTokenHash(context.Context, string, time.Time) (identity.Session, error)
	CreateRefreshToken(context.Context, identity.RefreshToken) error
	GetRefreshTokenByHash(context.Context, string) (identity.RefreshToken, error)
	CreateCredentials(context.Context, identity.Session, identity.RefreshToken) error
	RotateRefreshToken(context.Context, string, time.Time, identity.Session, identity.RefreshToken) error
	RevokeSession(context.Context, string) error
	RevokeAllSessions(context.Context, string) error
	PruneExpiredCredentials(context.Context, time.Time, int) (sessionsDeleted, refreshTokensDeleted int64, err error)
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

// CleanupResult 是一次凭证维护批次的结果。删除数量用于日志、指标和运行手册中的验收，
// 不把 token hash 或用户身份暴露到维护日志里。
type CleanupResult struct {
	SessionsDeleted      int64
	RefreshTokensDeleted int64
}

type Service struct {
	store      Store
	passwords  PasswordHasher
	ids        IDGenerator
	tokens     TokenGenerator
	clock      Clock
	accessTTL  time.Duration
	refreshTTL time.Duration
}

func NewService(
	store Store,
	passwords PasswordHasher,
	ids IDGenerator,
	tokens TokenGenerator,
	clock Clock,
	accessTTL, refreshTTL time.Duration,
) *Service {
	return &Service{
		store: store, passwords: passwords, ids: ids, tokens: tokens,
		clock: clock, accessTTL: accessTTL, refreshTTL: refreshTTL,
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
	User             identity.User
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
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

	accessToken, err := s.tokens.NewToken()
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate access token: %w", err)
	}
	refreshToken, err := s.tokens.NewToken()
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate refresh token: %w", err)
	}
	familyID, err := s.ids.NewID()
	if err != nil {
		return LoginResult{}, fmt.Errorf("generate credential family ID: %w", err)
	}
	createdAt := s.clock.Now()
	accessExpiresAt := createdAt.Add(s.accessTTL)
	refreshExpiresAt := createdAt.Add(s.refreshTTL)
	session, err := identity.NewSessionForFamily(
		hashToken(accessToken), user.ID(), familyID, createdAt, accessExpiresAt,
	)
	if err != nil {
		return LoginResult{}, fmt.Errorf("create access session: %w", err)
	}
	refresh, err := identity.NewRefreshToken(
		hashToken(refreshToken), user.ID(), familyID, createdAt, refreshExpiresAt,
	)
	if err != nil {
		return LoginResult{}, fmt.Errorf("create refresh token: %w", err)
	}
	if err := s.store.CreateCredentials(ctx, session, refresh); err != nil {
		return LoginResult{}, fmt.Errorf("save login credentials: %w", err)
	}

	return LoginResult{
		User:             user,
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     refreshToken,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

type RefreshResult struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// Refresh 只接受 Refresh Token，并通过一次原子轮换同时消费旧 Token、
// 创建新的 Access Session 和新的 Refresh Token。
func (s *Service) Refresh(ctx context.Context, token string) (RefreshResult, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return RefreshResult{}, ErrInvalidRefresh
	}

	now := s.clock.Now()
	current, err := s.store.GetRefreshTokenByHash(ctx, hashToken(token))
	if errors.Is(err, identity.ErrRefreshTokenNotFound) ||
		errors.Is(err, identity.ErrSessionNotFound) {
		return RefreshResult{}, ErrInvalidRefresh
	}
	if err != nil {
		return RefreshResult{}, fmt.Errorf("get refresh token: %w", err)
	}
	if !current.ExpiresAt().After(now) || current.IsRevoked() {
		return RefreshResult{}, ErrInvalidRefresh
	}

	accessToken, err := s.tokens.NewToken()
	if err != nil {
		return RefreshResult{}, fmt.Errorf("generate access token: %w", err)
	}
	newRefreshToken, err := s.tokens.NewToken()
	if err != nil {
		return RefreshResult{}, fmt.Errorf("generate refresh token: %w", err)
	}
	accessExpiresAt := now.Add(s.accessTTL)
	refreshExpiresAt := now.Add(s.refreshTTL)
	access, err := identity.NewSessionForFamily(
		hashToken(accessToken), current.UserID(), current.FamilyID(), now, accessExpiresAt,
	)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("create refreshed access session: %w", err)
	}
	replacement, err := identity.NewRefreshToken(
		hashToken(newRefreshToken), current.UserID(), current.FamilyID(), now, refreshExpiresAt,
	)
	if err != nil {
		return RefreshResult{}, fmt.Errorf("create replacement refresh token: %w", err)
	}

	if err := s.store.RotateRefreshToken(
		ctx, current.TokenHash(), now, access, replacement,
	); errors.Is(err, identity.ErrRefreshTokenReused) {
		// 旧 Token 被再次使用，说明可能发生了窃取或并发重放。
		// 存储层会在同一原子操作中撤销整个 family，避免攻击者继续使用新 Token。
		return RefreshResult{}, ErrInvalidRefresh
	} else if errors.Is(err, identity.ErrRefreshTokenRevoked) ||
		errors.Is(err, identity.ErrRefreshTokenNotFound) {
		return RefreshResult{}, ErrInvalidRefresh
	} else if err != nil {
		return RefreshResult{}, fmt.Errorf("rotate refresh token: %w", err)
	}

	return RefreshResult{
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     newRefreshToken,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (string, error) {
	principal, err := s.AuthenticatePrincipal(ctx, token)
	if err != nil {
		return "", err
	}
	return principal.UserID, nil
}

func (s *Service) AuthenticatePrincipal(
	ctx context.Context, token string,
) (authorization.Principal, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return authorization.Principal{}, ErrInvalidSession
	}

	session, err := s.store.GetSessionByTokenHash(ctx, hashToken(token), s.clock.Now())
	if errors.Is(err, identity.ErrSessionNotFound) {
		return authorization.Principal{}, ErrInvalidSession
	}
	if err != nil {
		return authorization.Principal{}, fmt.Errorf("get session: %w", err)
	}
	role, err := s.store.GetUserRole(ctx, session.UserID())
	if errors.Is(err, identity.ErrUserNotFound) {
		return authorization.Principal{}, ErrInvalidSession
	}
	if err != nil {
		return authorization.Principal{}, fmt.Errorf("get user role: %w", err)
	}
	return authorization.Principal{UserID: session.UserID(), Role: role}, nil
}

// Logout 撤销当前 Token。删除不存在的记录也视为成功，
// 让存储层的撤销操作具备幂等语义。HTTP 入口仍会先验证 Token 是否有效。
func (s *Service) Logout(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrInvalidSession
	}

	if err := s.store.RevokeSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// LogoutAll 撤销用户的全部会话，用于用户修改安全状态或怀疑凭证泄露时
// 立即让所有设备下线。
func (s *Service) LogoutAll(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ErrInvalidSession
	}

	if err := s.store.RevokeAllSessions(ctx, userID); err != nil {
		return fmt.Errorf("revoke all sessions: %w", err)
	}
	return nil
}

// PruneExpiredCredentials 负责认证凭证的物理回收。
//
// 认证请求只需要“逻辑上忽略已过期凭证”，但数据库中的旧记录仍会持续占用空间，
// 也会让索引和备份不断增长。因此清理作为独立维护用例，由后台 Worker 周期调用。
func (s *Service) PruneExpiredCredentials(
	ctx context.Context, now time.Time, batchSize int,
) (CleanupResult, error) {
	if s == nil || s.store == nil {
		return CleanupResult{}, errors.New("auth service is not configured")
	}

	sessionsDeleted, refreshTokensDeleted, err := s.store.PruneExpiredCredentials(ctx, now, batchSize)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("prune expired credentials: %w", err)
	}
	return CleanupResult{
		SessionsDeleted:      sessionsDeleted,
		RefreshTokensDeleted: refreshTokensDeleted,
	}, nil
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
