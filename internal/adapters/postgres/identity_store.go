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
		INSERT INTO sessions (token_hash, user_id, family_id, created_at, expires_at)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5)
	`, session.TokenHash(), session.UserID(), session.FamilyID(), session.CreatedAt(), session.ExpiresAt())
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *IdentityStore) CreateRefreshToken(ctx context.Context, token identity.RefreshToken) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, family_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, token.TokenHash(), token.UserID(), token.FamilyID(), token.CreatedAt(), token.ExpiresAt())
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

// CreateCredentials 在一个事务中写入登录产生的 Access Session 和 Refresh Token。
// 不能先写其中一个再写另一个，否则中途失败会留下半套凭证。
func (s *IdentityStore) CreateCredentials(
	ctx context.Context,
	session identity.Session,
	refresh identity.RefreshToken,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin credential transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, family_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, session.TokenHash(), session.UserID(), session.FamilyID(), session.CreatedAt(), session.ExpiresAt()); err != nil {
		return fmt.Errorf("insert access session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, family_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, refresh.TokenHash(), refresh.UserID(), refresh.FamilyID(), refresh.CreatedAt(), refresh.ExpiresAt()); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit credentials: %w", err)
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
		familyID  sql.NullString
		createdAt time.Time
		expiresAt time.Time
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, user_id, family_id, created_at, expires_at
		FROM sessions
		WHERE token_hash = $1 AND expires_at > $2
	`, tokenHash, now).Scan(
		&record.tokenHash, &record.userID, &record.familyID, &record.createdAt, &record.expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.Session{}, identity.ErrSessionNotFound
	}
	if err != nil {
		return identity.Session{}, fmt.Errorf("select session: %w", err)
	}

	session, err := identity.RestoreSessionForFamily(
		record.tokenHash, record.userID, record.familyID.String, record.createdAt, record.expiresAt,
	)
	if err != nil {
		return identity.Session{}, fmt.Errorf("restore session: %w", err)
	}
	return session, nil
}

func (s *IdentityStore) GetRefreshTokenByHash(
	ctx context.Context,
	tokenHash string,
) (identity.RefreshToken, error) {
	var record struct {
		tokenHash string
		userID    string
		familyID  string
		createdAt time.Time
		expiresAt time.Time
		usedAt    sql.NullTime
		revokedAt sql.NullTime
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT token_hash, user_id, family_id, created_at, expires_at, used_at, revoked_at
		FROM refresh_tokens
		WHERE token_hash = $1
	`, tokenHash).Scan(
		&record.tokenHash, &record.userID, &record.familyID, &record.createdAt,
		&record.expiresAt, &record.usedAt, &record.revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.RefreshToken{}, identity.ErrRefreshTokenNotFound
	}
	if err != nil {
		return identity.RefreshToken{}, fmt.Errorf(`select refresh token: %w`, err)
	}

	var usedAt, revokedAt time.Time
	if record.usedAt.Valid {
		usedAt = record.usedAt.Time
	}
	if record.revokedAt.Valid {
		revokedAt = record.revokedAt.Time
	}
	token, err := identity.RestoreRefreshToken(
		record.tokenHash, record.userID, record.familyID, record.createdAt,
		record.expiresAt, usedAt, revokedAt,
	)
	if err != nil {
		return identity.RefreshToken{}, fmt.Errorf(`restore refresh token: %w`, err)
	}
	return token, nil
}

// RotateRefreshToken 使用行锁串行化同一个 Refresh Token 的并发消费。
// 第一个请求把 used_at 写入并创建替代凭证；后续请求发现已使用，
// 立即撤销整个 family，阻止已经泄露的新 Token 继续工作。
func (s *IdentityStore) RotateRefreshToken(
	ctx context.Context,
	tokenHash string,
	usedAt time.Time,
	access identity.Session,
	replacement identity.RefreshToken,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(`begin refresh transaction: %w`, err)
	}
	defer func() { _ = tx.Rollback() }()

	var record struct {
		familyID  string
		usedAt    sql.NullTime
		revokedAt sql.NullTime
	}
	err = tx.QueryRowContext(ctx, `
		SELECT family_id, used_at, revoked_at
		FROM refresh_tokens
		WHERE token_hash = $1
		FOR UPDATE
	`, tokenHash).Scan(&record.familyID, &record.usedAt, &record.revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.ErrRefreshTokenNotFound
	}
	if err != nil {
		return fmt.Errorf(`lock refresh token: %w`, err)
	}
	if record.revokedAt.Valid {
		return identity.ErrRefreshTokenRevoked
	}
	if record.usedAt.Valid {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM sessions WHERE family_id = $1
		`, record.familyID); err != nil {
			return fmt.Errorf(`revoke reused token family: %w`, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE refresh_tokens
			SET revoked_at = COALESCE(revoked_at, $2)
			WHERE family_id = $1
		`, record.familyID, usedAt); err != nil {
			return fmt.Errorf(`mark reused token family revoked: %w`, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf(`commit reused token revocation: %w`, err)
		}
		return identity.ErrRefreshTokenReused
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE refresh_tokens SET used_at = $1 WHERE token_hash = $2
	`, usedAt, tokenHash); err != nil {
		return fmt.Errorf(`mark refresh token used: %w`, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, family_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, access.TokenHash(), access.UserID(), access.FamilyID(), access.CreatedAt(), access.ExpiresAt()); err != nil {
		return fmt.Errorf(`insert refreshed access session: %w`, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO refresh_tokens (token_hash, user_id, family_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)
	`, replacement.TokenHash(), replacement.UserID(), replacement.FamilyID(), replacement.CreatedAt(), replacement.ExpiresAt()); err != nil {
		return fmt.Errorf(`insert replacement refresh token: %w`, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf(`commit refresh rotation: %w`, err)
	}
	return nil
}

func (s *IdentityStore) RevokeSession(ctx context.Context, tokenHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var familyID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT family_id FROM sessions WHERE token_hash = $1`, tokenHash).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find session: %w", err)
	}
	if familyID.Valid {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE family_id = $1`, familyID.String); err != nil {
			return fmt.Errorf("delete session family: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE family_id = $1`, familyID.String); err != nil {
			return fmt.Errorf("delete refresh token family: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session revocation: %w", err)
	}
	return nil
}

func (s *IdentityStore) RevokeAllSessions(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin user session revocation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete user refresh tokens: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit user session revocation: %w", err)
	}
	return nil
}

// PruneExpiredCredentials 在一个短事务中各清理一批过期凭证。
// CTE 先按 expires_at 选出有限数量的主键，再执行 DELETE，避免一次维护任务
// 因历史积压锁住整张表过久；两张表都使用已有的 expires_at 索引。
func (s *IdentityStore) PruneExpiredCredentials(
	ctx context.Context, now time.Time, batchSize int,
) (sessionsDeleted, refreshTokensDeleted int64, err error) {
	if batchSize <= 0 {
		return 0, 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin credential cleanup transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		WITH expired AS (
			SELECT token_hash
			FROM sessions
			WHERE expires_at <= $1
			ORDER BY expires_at, token_hash
			LIMIT $2
		)
		DELETE FROM sessions
		WHERE token_hash IN (SELECT token_hash FROM expired)
	`, now, batchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	sessionsDeleted, err = result.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count deleted sessions: %w", err)
	}

	result, err = tx.ExecContext(ctx, `
		WITH expired AS (
			SELECT token_hash
			FROM refresh_tokens
			WHERE expires_at <= $1
			ORDER BY expires_at, token_hash
			LIMIT $2
		)
		DELETE FROM refresh_tokens
		WHERE token_hash IN (SELECT token_hash FROM expired)
	`, now, batchSize)
	if err != nil {
		return 0, 0, fmt.Errorf("delete expired refresh tokens: %w", err)
	}
	refreshTokensDeleted, err = result.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count deleted refresh tokens: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit credential cleanup: %w", err)
	}
	return sessionsDeleted, refreshTokensDeleted, nil
}
