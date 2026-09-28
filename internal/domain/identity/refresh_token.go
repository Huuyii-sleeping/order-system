package identity

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidRefreshFamilyID = errors.New("invalid refresh token family ID")
	ErrRefreshTokenNotFound   = errors.New("refresh token not found")
	ErrRefreshTokenReused     = errors.New("refresh token reused")
	ErrRefreshTokenRevoked    = errors.New("refresh token revoked")
)

// RefreshToken 是只能用于换取新凭证的长期凭证。
// usedAt 用来记录轮换是否已经消费过它，revokedAt 用来记录整组凭证是否被撤销。
type RefreshToken struct {
	tokenHash string
	userID    string
	familyID  string
	createdAt time.Time
	expiresAt time.Time
	usedAt    time.Time
	revokedAt time.Time
}

func NewRefreshToken(
	tokenHash, userID, familyID string,
	createdAt, expiresAt time.Time,
) (RefreshToken, error) {
	return RestoreRefreshToken(
		tokenHash, userID, familyID, createdAt, expiresAt, time.Time{}, time.Time{},
	)
}

func RestoreRefreshToken(
	tokenHash, userID, familyID string,
	createdAt, expiresAt, usedAt, revokedAt time.Time,
) (RefreshToken, error) {
	tokenHash = strings.TrimSpace(tokenHash)
	userID = strings.TrimSpace(userID)
	familyID = strings.TrimSpace(familyID)

	decoded, err := hex.DecodeString(tokenHash)
	switch {
	case err != nil || len(decoded) != 32:
		return RefreshToken{}, ErrInvalidTokenHash
	case userID == "":
		return RefreshToken{}, ErrInvalidUserID
	case familyID == "":
		return RefreshToken{}, ErrInvalidRefreshFamilyID
	case createdAt.IsZero():
		return RefreshToken{}, ErrInvalidCreatedAt
	case !expiresAt.After(createdAt):
		return RefreshToken{}, ErrInvalidExpiresAt
	}

	return RefreshToken{
		tokenHash: tokenHash,
		userID:    userID,
		familyID:  familyID,
		createdAt: createdAt,
		expiresAt: expiresAt,
		usedAt:    usedAt,
		revokedAt: revokedAt,
	}, nil
}

func (t RefreshToken) TokenHash() string    { return t.tokenHash }
func (t RefreshToken) UserID() string       { return t.userID }
func (t RefreshToken) FamilyID() string     { return t.familyID }
func (t RefreshToken) CreatedAt() time.Time { return t.createdAt }
func (t RefreshToken) ExpiresAt() time.Time { return t.expiresAt }
func (t RefreshToken) UsedAt() time.Time    { return t.usedAt }
func (t RefreshToken) RevokedAt() time.Time { return t.revokedAt }
func (t RefreshToken) IsUsed() bool         { return !t.usedAt.IsZero() }
func (t RefreshToken) IsRevoked() bool      { return !t.revokedAt.IsZero() }

func (t RefreshToken) MarkUsed(at time.Time) (RefreshToken, error) {
	if t.IsUsed() {
		return RefreshToken{}, ErrRefreshTokenReused
	}
	if t.IsRevoked() {
		return RefreshToken{}, ErrRefreshTokenRevoked
	}
	if at.IsZero() {
		return RefreshToken{}, ErrInvalidCreatedAt
	}
	t.usedAt = at
	return t, nil
}

func (t RefreshToken) Revoke(at time.Time) (RefreshToken, error) {
	if at.IsZero() {
		return RefreshToken{}, ErrInvalidCreatedAt
	}
	t.revokedAt = at
	return t, nil
}
