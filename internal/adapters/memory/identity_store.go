package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"example.com/order-system/internal/application/authorization"
	"example.com/order-system/internal/domain/identity"
)

// IdentityStore 是认证端口的进程内实现，便于本地启动和快速测试。
type IdentityStore struct {
	mu sync.RWMutex

	usersByEmail        map[string]identity.User
	rolesByUserID       map[string]authorization.Role
	sessionsByTokenHash map[string]identity.Session
	refreshByTokenHash  map[string]identity.RefreshToken
}

func NewIdentityStore() *IdentityStore {
	return &IdentityStore{
		usersByEmail:        make(map[string]identity.User),
		rolesByUserID:       make(map[string]authorization.Role),
		sessionsByTokenHash: make(map[string]identity.Session),
		refreshByTokenHash:  make(map[string]identity.RefreshToken),
	}
}

func (s *IdentityStore) CreateUser(ctx context.Context, user identity.User) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.usersByEmail[user.Email()]; exists {
		return identity.ErrEmailAlreadyExists
	}

	s.usersByEmail[user.Email()] = user
	s.rolesByUserID[user.ID()] = authorization.RoleCustomer
	return nil
}

func (s *IdentityStore) GetUserRole(
	ctx context.Context, userID string,
) (authorization.Role, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, exists := s.rolesByUserID[userID]
	if !exists {
		return "", identity.ErrUserNotFound
	}
	return role, nil
}

// SetUserRole supports local learning and tests. Production role changes must
// use an audited administration workflow instead of trusting registration input.
func (s *IdentityStore) SetUserRole(userID string, role authorization.Role) error {
	if _, ok := authorization.ParseRole(string(role)); !ok {
		return errors.New("invalid role")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rolesByUserID[userID]; !exists {
		return identity.ErrUserNotFound
	}
	s.rolesByUserID[userID] = role
	return nil
}

func (s *IdentityStore) GetUserByEmail(ctx context.Context, email string) (identity.User, error) {
	if err := ctx.Err(); err != nil {
		return identity.User{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	user, exists := s.usersByEmail[email]
	if !exists {
		return identity.User{}, identity.ErrUserNotFound
	}
	return user, nil
}

func (s *IdentityStore) CreateSession(ctx context.Context, session identity.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionsByTokenHash[session.TokenHash()] = session
	return nil
}

func (s *IdentityStore) CreateRefreshToken(ctx context.Context, token identity.RefreshToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshByTokenHash[token.TokenHash()] = token
	return nil
}

func (s *IdentityStore) CreateCredentials(
	ctx context.Context,
	session identity.Session,
	refresh identity.RefreshToken,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionsByTokenHash[session.TokenHash()] = session
	s.refreshByTokenHash[refresh.TokenHash()] = refresh
	return nil
}

func (s *IdentityStore) GetSessionByTokenHash(
	ctx context.Context,
	tokenHash string,
	now time.Time,
) (identity.Session, error) {
	if err := ctx.Err(); err != nil {
		return identity.Session{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	session, exists := s.sessionsByTokenHash[tokenHash]
	if !exists || !session.ExpiresAt().After(now) {
		return identity.Session{}, identity.ErrSessionNotFound
	}
	return session, nil
}

func (s *IdentityStore) GetRefreshTokenByHash(
	ctx context.Context,
	tokenHash string,
) (identity.RefreshToken, error) {
	if err := ctx.Err(); err != nil {
		return identity.RefreshToken{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	token, exists := s.refreshByTokenHash[tokenHash]
	if !exists {
		return identity.RefreshToken{}, identity.ErrRefreshTokenNotFound
	}
	return token, nil
}

func (s *IdentityStore) RotateRefreshToken(
	ctx context.Context,
	tokenHash string,
	usedAt time.Time,
	access identity.Session,
	replacement identity.RefreshToken,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.refreshByTokenHash[tokenHash]
	if !exists {
		return identity.ErrRefreshTokenNotFound
	}
	if current.IsRevoked() {
		return identity.ErrRefreshTokenRevoked
	}
	if current.IsUsed() {
		s.revokeFamilyLocked(current.FamilyID(), usedAt)
		return identity.ErrRefreshTokenReused
	}

	updated, err := current.MarkUsed(usedAt)
	if err != nil {
		return err
	}
	s.refreshByTokenHash[tokenHash] = updated
	s.sessionsByTokenHash[access.TokenHash()] = access
	s.refreshByTokenHash[replacement.TokenHash()] = replacement
	return nil
}

func (s *IdentityStore) RevokeSession(ctx context.Context, tokenHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if session, exists := s.sessionsByTokenHash[tokenHash]; exists && session.FamilyID() != "" {
		s.revokeFamilyLocked(session.FamilyID(), time.Now())
	} else {
		delete(s.sessionsByTokenHash, tokenHash)
	}
	return nil
}

func (s *IdentityStore) RevokeAllSessions(ctx context.Context, userID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for tokenHash, session := range s.sessionsByTokenHash {
		if session.UserID() == userID {
			delete(s.sessionsByTokenHash, tokenHash)
		}
	}
	for tokenHash, token := range s.refreshByTokenHash {
		if token.UserID() == userID {
			delete(s.refreshByTokenHash, tokenHash)
		}
	}
	return nil
}

// PruneExpiredCredentials 按批次删除已经过期的凭证，模拟数据库维护任务的有界工作量。
// 生产 PostgreSQL 实现会依赖 expires_at 索引；内存实现保持相同的端口语义，便于测试。
func (s *IdentityStore) PruneExpiredCredentials(
	ctx context.Context, now time.Time, batchSize int,
) (sessionsDeleted, refreshTokensDeleted int64, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if batchSize <= 0 {
		return 0, 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for tokenHash, session := range s.sessionsByTokenHash {
		if sessionsDeleted >= int64(batchSize) {
			break
		}
		if !session.ExpiresAt().After(now) {
			delete(s.sessionsByTokenHash, tokenHash)
			sessionsDeleted++
		}
	}
	for tokenHash, token := range s.refreshByTokenHash {
		if refreshTokensDeleted >= int64(batchSize) {
			break
		}
		if !token.ExpiresAt().After(now) {
			delete(s.refreshByTokenHash, tokenHash)
			refreshTokensDeleted++
		}
	}
	return sessionsDeleted, refreshTokensDeleted, nil
}

func (s *IdentityStore) revokeFamilyLocked(familyID string, at time.Time) {
	for tokenHash, session := range s.sessionsByTokenHash {
		if session.FamilyID() == familyID {
			delete(s.sessionsByTokenHash, tokenHash)
		}
	}
	for tokenHash, token := range s.refreshByTokenHash {
		if token.FamilyID() != familyID {
			continue
		}
		revoked, err := token.Revoke(at)
		if err == nil {
			s.refreshByTokenHash[tokenHash] = revoked
		}
	}
}
