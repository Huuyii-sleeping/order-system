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
