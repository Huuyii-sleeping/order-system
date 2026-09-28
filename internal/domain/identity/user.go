// Package identity 保存用户身份与登录会话的核心模型。
package identity

import (
	"errors"
	"net/mail"
	"strings"
	"time"
)

var (
	ErrInvalidUserID       = errors.New("invalid user ID")
	ErrInvalidEmail        = errors.New("invalid email")
	ErrInvalidPassword     = errors.New("invalid password")
	ErrInvalidPasswordHash = errors.New("invalid password hash")
	ErrInvalidCreatedAt    = errors.New("invalid created at")
	ErrUserNotFound        = errors.New("user not found")
	ErrEmailAlreadyExists  = errors.New("email already exists")
)

// User 只保存密码哈希，不保存也不暴露明文密码。
type User struct {
	id           string
	email        string
	passwordHash string
	createdAt    time.Time
}

func NewUser(id, email, passwordHash string, createdAt time.Time) (User, error) {
	return RestoreUser(id, email, passwordHash, createdAt)
}

func RestoreUser(id, email, passwordHash string, createdAt time.Time) (User, error) {
	id = strings.TrimSpace(id)
	email, err := NormalizeEmail(email)
	if err != nil {
		return User{}, err
	}

	switch {
	case id == "":
		return User{}, ErrInvalidUserID
	case strings.TrimSpace(passwordHash) == "":
		return User{}, ErrInvalidPasswordHash
	case createdAt.IsZero():
		return User{}, ErrInvalidCreatedAt
	}

	return User{id: id, email: email, passwordHash: passwordHash, createdAt: createdAt}, nil
}

// NormalizeEmail 让注册、登录和数据库唯一约束使用同一种规范形式。
func NormalizeEmail(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" || len(normalized) > 254 {
		return "", ErrInvalidEmail
	}

	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Address != normalized {
		return "", ErrInvalidEmail
	}

	return normalized, nil
}

func (u User) ID() string           { return u.id }
func (u User) Email() string        { return u.email }
func (u User) PasswordHash() string { return u.passwordHash }
func (u User) CreatedAt() time.Time { return u.createdAt }
