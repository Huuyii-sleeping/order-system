package identity

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidTokenHash = errors.New("invalid session token hash")
	ErrInvalidExpiresAt = errors.New("invalid session expiration")
	ErrSessionNotFound  = errors.New("session not found")
)

// Session 保存 Token 摘要与所属用户。原始 Bearer Token 只返回给客户端一次。
type Session struct {
	tokenHash string
	userID    string
	createdAt time.Time
	expiresAt time.Time
}

func NewSession(tokenHash, userID string, createdAt, expiresAt time.Time) (Session, error) {
	return RestoreSession(tokenHash, userID, createdAt, expiresAt)
}

func RestoreSession(tokenHash, userID string, createdAt, expiresAt time.Time) (Session, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	userID = strings.TrimSpace(userID)

	decoded, err := hex.DecodeString(tokenHash)
	switch {
	case err != nil || len(decoded) != 32:
		return Session{}, ErrInvalidTokenHash
	case userID == "":
		return Session{}, ErrInvalidUserID
	case createdAt.IsZero():
		return Session{}, ErrInvalidCreatedAt
	case !expiresAt.After(createdAt):
		return Session{}, ErrInvalidExpiresAt
	}

	return Session{
		tokenHash: tokenHash,
		userID:    userID,
		createdAt: createdAt,
		expiresAt: expiresAt,
	}, nil
}

func (s Session) TokenHash() string    { return s.tokenHash }
func (s Session) UserID() string       { return s.userID }
func (s Session) CreatedAt() time.Time { return s.createdAt }
func (s Session) ExpiresAt() time.Time { return s.expiresAt }
