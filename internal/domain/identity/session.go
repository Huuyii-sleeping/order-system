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
	familyID  string
	createdAt time.Time
	expiresAt time.Time
}

func NewSession(tokenHash, userID string, createdAt, expiresAt time.Time) (Session, error) {
	return NewSessionForFamily(tokenHash, userID, "", createdAt, expiresAt)
}

func RestoreSession(tokenHash, userID string, createdAt, expiresAt time.Time) (Session, error) {
	return RestoreSessionForFamily(tokenHash, userID, "", createdAt, expiresAt)
}

// NewSessionForFamily 把 Access Session 和 Refresh Token 放进同一个凭证家族。
// 这样登出当前设备时，存储层可以同时撤销这一对凭证。
func NewSessionForFamily(
	tokenHash, userID, familyID string,
	createdAt, expiresAt time.Time,
) (Session, error) {
	return RestoreSessionForFamily(tokenHash, userID, familyID, createdAt, expiresAt)
}

func RestoreSessionForFamily(
	tokenHash, userID, familyID string,
	createdAt, expiresAt time.Time,
) (Session, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	userID = strings.TrimSpace(userID)
	familyID = strings.TrimSpace(familyID)

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
		familyID:  familyID,
		createdAt: createdAt,
		expiresAt: expiresAt,
	}, nil
}

func (s Session) TokenHash() string    { return s.tokenHash }
func (s Session) UserID() string       { return s.userID }
func (s Session) FamilyID() string     { return s.familyID }
func (s Session) CreatedAt() time.Time { return s.createdAt }
func (s Session) ExpiresAt() time.Time { return s.expiresAt }
