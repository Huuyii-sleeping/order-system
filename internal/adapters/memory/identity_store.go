package memory

import (
	"context"
	"sync"
	"time"

	"example.com/order-system/internal/domain/identity"
)

// IdentityStore 是认证端口的进程内实现，便于本地启动和快速测试。
type IdentityStore struct {
	mu sync.RWMutex

	usersByEmail        map[string]identity.User
	sessionsByTokenHash map[string]identity.Session
}

func NewIdentityStore() *IdentityStore {
	return &IdentityStore{
		usersByEmail:        make(map[string]identity.User),
		sessionsByTokenHash: make(map[string]identity.Session),
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

func (s *IdentityStore) RevokeSession(ctx context.Context, tokenHash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessionsByTokenHash, tokenHash)
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
	return nil
}
