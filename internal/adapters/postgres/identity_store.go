package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"example.com/order-system/internal/domain/identity"
)

const usersEmailConstraint = "users_email_unique"

type IdentityStore struct {
	db *sql.DB
}

func NewIdentityStore(db *sql.DB) *IdentityStore {
	return &IdentityStore{db: db}
}

func (s *IdentityStore) CreateUser(ctx context.Context, user identity.User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (id, email, password_hash, created_at)
		VALUES ($1, $2, $3, $4)
	`, user.ID(), user.Email(), user.PasswordHash(), user.CreatedAt())
	if err == nil {
		return nil
	}

	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		postgresError.Code == "23505" &&
		postgresError.ConstraintName == usersEmailConstraint {
		return identity.ErrEmailAlreadyExists
	}
	return fmt.Errorf("insert user: %w", err)
}

func (s *IdentityStore) GetUserByEmail(ctx context.Context, email string) (identity.User, error) {
	var record struct {
		id           string
		email        string
		passwordHash string
		createdAt    time.Time
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, created_at
		FROM users
		WHERE email = $1
	`, email).Scan(&record.id, &record.email, &record.passwordHash, &record.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.User{}, identity.ErrUserNotFound
	}
	if err != nil {
		return identity.User{}, fmt.Errorf("select user by email: %w", err)
	}

	user, err := identity.RestoreUser(record.id, record.email, record.passwordHash, record.createdAt)
	if err != nil {
		return identity.User{}, fmt.Errorf("restore user: %w", err)
	}
	return user, nil
}

func (s *IdentityStore) CreateSession(ctx context.Context, session identity.Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4)
	`, session.TokenHash(), session.UserID(), session.CreatedAt(), session.ExpiresAt())
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *IdentityStore) GetSessionByTokenHash(
	ctx context.Context,
	tokenHash string,
	now time.Time,
) (identity.Session, error) {
	var record struct {
		tokenHash string
		userID    string
		createdAt time.Time
		expiresAt time.Time
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, user_id, created_at, expires_at
		FROM sessions
		WHERE token_hash = $1 AND expires_at > $2
	`, tokenHash, now).Scan(
		&record.tokenHash, &record.userID, &record.createdAt, &record.expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Session{}, identity.ErrSessionNotFound
	}
	if err != nil {
		return identity.Session{}, fmt.Errorf("select session: %w", err)
	}

	session, err := identity.RestoreSession(
		record.tokenHash, record.userID, record.createdAt, record.expiresAt,
	)
	if err != nil {
		return identity.Session{}, fmt.Errorf("restore session: %w", err)
	}
	return session, nil
}
