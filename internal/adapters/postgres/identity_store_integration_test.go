package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/domain/identity"
	"example.com/order-system/migrations"
)

func TestIntegrationIdentityStore(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	store := postgres.NewIdentityStore(db)
	now := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)

	user, err := identity.NewUser("user-1", "learner@example.com", "bcrypt-hash", now)
	if err != nil {
		t.Fatalf("create user fixture: %v", err)
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	stored, err := store.GetUserByEmail(ctx, user.Email())
	if err != nil || stored.ID() != user.ID() || stored.PasswordHash() != user.PasswordHash() {
		t.Fatalf("GetUserByEmail() = %+v, %v", stored, err)
	}

	t.Run("concurrent duplicate registration has one winner", func(t *testing.T) {
		const requests = 12
		var created atomic.Int32
		var duplicates atomic.Int32
		var waitGroup sync.WaitGroup
		for index := range requests {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				createdUser, createErr := identity.NewUser(
					fmt.Sprintf("concurrent-user-%d", index),
					"duplicate@example.com", "bcrypt-hash", now,
				)
				if createErr != nil {
					t.Errorf("NewUser() error = %v", createErr)
					return
				}
				switch err := store.CreateUser(ctx, createdUser); {
				case err == nil:
					created.Add(1)
				case errors.Is(err, identity.ErrEmailAlreadyExists):
					duplicates.Add(1)
				default:
					t.Errorf("CreateUser() error = %v", err)
				}
			}()
		}
		waitGroup.Wait()
		if created.Load() != 1 || duplicates.Load() != requests-1 {
			t.Errorf("created = %d, duplicates = %d", created.Load(), duplicates.Load())
		}
	})

	t.Run("session lookup excludes expired sessions", func(t *testing.T) {
		const tokenHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		session, createErr := identity.NewSession(tokenHash, user.ID(), now, now.Add(time.Hour))
		if createErr != nil {
			t.Fatalf("NewSession() error = %v", createErr)
		}
		if err := store.CreateSession(ctx, session); err != nil {
			t.Fatalf("CreateSession() error = %v", err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, tokenHash, now); err != nil {
			t.Fatalf("active GetSession() error = %v", err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, tokenHash, now.Add(time.Hour)); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("expired GetSession() error = %v", err)
		}
	})

	t.Run("prunes expired credentials in bounded batches", func(t *testing.T) {
		expiredSession, err := identity.NewSession(
			"1212121212121212121212121212121212121212121212121212121212121212",
			user.ID(), now.Add(-2*time.Hour), now.Add(-time.Hour),
		)
		if err != nil {
			t.Fatalf("create expired session: %v", err)
		}
		activeSession, err := identity.NewSession(
			"1313131313131313131313131313131313131313131313131313131313131313",
			user.ID(), now, now.Add(time.Hour),
		)
		if err != nil {
			t.Fatalf("create active session: %v", err)
		}
		expiredRefresh, err := identity.NewRefreshToken(
			"1414141414141414141414141414141414141414141414141414141414141414",
			user.ID(), "cleanup-family-expired", now.Add(-2*time.Hour), now.Add(-time.Hour),
		)
		if err != nil {
			t.Fatalf("create expired refresh token: %v", err)
		}
		activeRefresh, err := identity.NewRefreshToken(
			"1515151515151515151515151515151515151515151515151515151515151515",
			user.ID(), "cleanup-family-active", now, now.Add(time.Hour),
		)
		if err != nil {
			t.Fatalf("create active refresh token: %v", err)
		}
		if err := store.CreateSession(ctx, expiredSession); err != nil {
			t.Fatalf("CreateSession(expired) error = %v", err)
		}
		if err := store.CreateSession(ctx, activeSession); err != nil {
			t.Fatalf("CreateSession(active) error = %v", err)
		}
		if err := store.CreateRefreshToken(ctx, expiredRefresh); err != nil {
			t.Fatalf("CreateRefreshToken(expired) error = %v", err)
		}
		if err := store.CreateRefreshToken(ctx, activeRefresh); err != nil {
			t.Fatalf("CreateRefreshToken(active) error = %v", err)
		}

		sessionsDeleted, refreshTokensDeleted, err := store.PruneExpiredCredentials(ctx, now, 1)
		if err != nil {
			t.Fatalf("first PruneExpiredCredentials() error = %v", err)
		}
		if sessionsDeleted != 1 || refreshTokensDeleted != 1 {
			t.Fatalf("first deleted counts = %d, %d; want one of each", sessionsDeleted, refreshTokensDeleted)
		}
		secondSessionsDeleted, secondRefreshTokensDeleted, err := store.PruneExpiredCredentials(ctx, now, 1)
		if err != nil {
			t.Fatalf("second PruneExpiredCredentials() error = %v", err)
		}
		if secondSessionsDeleted != 0 || secondRefreshTokensDeleted != 0 {
			t.Fatalf("second deleted counts = %d, %d; want zero", secondSessionsDeleted, secondRefreshTokensDeleted)
		}
		if _, err := store.GetSessionByTokenHash(ctx, expiredSession.TokenHash(), now); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("expired session lookup error = %v, want not found", err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, activeSession.TokenHash(), now); err != nil {
			t.Fatalf("active session lookup error = %v", err)
		}
		if _, err := store.GetRefreshTokenByHash(ctx, expiredRefresh.TokenHash()); !errors.Is(err, identity.ErrRefreshTokenNotFound) {
			t.Fatalf("expired refresh lookup error = %v, want not found", err)
		}
		if _, err := store.GetRefreshTokenByHash(ctx, activeRefresh.TokenHash()); err != nil {
			t.Fatalf("active refresh lookup error = %v", err)
		}
	})

	t.Run("revokes one session or all user sessions", func(t *testing.T) {
		first, err := identity.NewSession(
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			user.ID(), now, now.Add(time.Hour),
		)
		if err != nil {
			t.Fatalf("create first session: %v", err)
		}
		second, err := identity.NewSession(
			"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
			user.ID(), now, now.Add(time.Hour),
		)
		if err != nil {
			t.Fatalf("create second session: %v", err)
		}
		if err := store.CreateSession(ctx, first); err != nil {
			t.Fatalf("CreateSession(first) error = %v", err)
		}
		if err := store.CreateSession(ctx, second); err != nil {
			t.Fatalf("CreateSession(second) error = %v", err)
		}
		if err := store.RevokeSession(ctx, first.TokenHash()); err != nil {
			t.Fatalf("RevokeSession() error = %v", err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, first.TokenHash(), now); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("revoked session lookup error = %v", err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, second.TokenHash(), now); err != nil {
			t.Fatalf("remaining session lookup error = %v", err)
		}
		if err := store.RevokeAllSessions(ctx, user.ID()); err != nil {
			t.Fatalf("RevokeAllSessions() error = %v", err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, second.TokenHash(), now); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("all-revoked session lookup error = %v", err)
		}
	})

	t.Run("rotates refresh tokens and revokes a replayed family", func(t *testing.T) {
		access, err := identity.NewSessionForFamily(
			"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			user.ID(), "family-1", now, now.Add(time.Hour),
		)
		if err != nil {
			t.Fatalf("create access session: %v", err)
		}
		refresh, err := identity.NewRefreshToken(
			"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
			user.ID(), "family-1", now, now.Add(24*time.Hour),
		)
		if err != nil {
			t.Fatalf("create refresh token: %v", err)
		}
		if err := store.CreateCredentials(ctx, access, refresh); err != nil {
			t.Fatalf("CreateCredentials() error = %v", err)
		}

		replacementAccess, err := identity.NewSessionForFamily(
			"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			user.ID(), "family-1", now.Add(time.Minute), now.Add(2*time.Hour),
		)
		if err != nil {
			t.Fatalf("create replacement access session: %v", err)
		}
		replacementRefresh, err := identity.NewRefreshToken(
			"1111111111111111111111111111111111111111111111111111111111111111",
			user.ID(), "family-1", now.Add(time.Minute), now.Add(24*time.Hour),
		)
		if err != nil {
			t.Fatalf("create replacement refresh token: %v", err)
		}
		usedAt := now.Add(time.Minute)
		if err := store.RotateRefreshToken(
			ctx, refresh.TokenHash(), usedAt, replacementAccess, replacementRefresh,
		); err != nil {
			t.Fatalf("first RotateRefreshToken() error = %v", err)
		}

		storedOld, err := store.GetRefreshTokenByHash(ctx, refresh.TokenHash())
		if err != nil || !storedOld.IsUsed() {
			t.Fatalf("old refresh token = %+v, %v; want used", storedOld, err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, replacementAccess.TokenHash(), now); err != nil {
			t.Fatalf("replacement access lookup error = %v", err)
		}

		secondAccess, err := identity.NewSessionForFamily(
			"2222222222222222222222222222222222222222222222222222222222222222",
			user.ID(), "family-1", now.Add(2*time.Minute), now.Add(3*time.Hour),
		)
		if err != nil {
			t.Fatalf("create second access session: %v", err)
		}
		secondRefresh, err := identity.NewRefreshToken(
			"3333333333333333333333333333333333333333333333333333333333333333",
			user.ID(), "family-1", now.Add(2*time.Minute), now.Add(24*time.Hour),
		)
		if err != nil {
			t.Fatalf("create second refresh token: %v", err)
		}
		if err := store.RotateRefreshToken(
			ctx, refresh.TokenHash(), usedAt.Add(time.Minute), secondAccess, secondRefresh,
		); !errors.Is(err, identity.ErrRefreshTokenReused) {
			t.Fatalf("replayed RotateRefreshToken() error = %v, want reused", err)
		}
		if _, err := store.GetSessionByTokenHash(ctx, replacementAccess.TokenHash(), now); !errors.Is(err, identity.ErrSessionNotFound) {
			t.Fatalf("replacement access after replay error = %v, want not found", err)
		}
		storedReplacement, err := store.GetRefreshTokenByHash(ctx, replacementRefresh.TokenHash())
		if err != nil || !storedReplacement.IsRevoked() {
			t.Fatalf("replacement refresh after replay = %+v, %v; want revoked", storedReplacement, err)
		}
	})
}

func openMigratedIdentityTestDB(t *testing.T, ctx context.Context, databaseURL string) *sql.DB {
	t.Helper()
	schema := fmt.Sprintf("identity_test_%d", time.Now().UnixNano())
	adminDB, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := adminDB.ExecContext(dropCtx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop schema: %v", err)
		}
	})

	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := postgres.Open(ctx, parsed.String())
	if err != nil {
		t.Fatalf("connect test schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := postgres.Migrate(ctx, db, migrations.Files); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}
