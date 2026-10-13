package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"example.com/order-system/internal/adapters/postgres"
)

func TestIntegrationMigrateSerializesConcurrentRunnersAndRejectsDrift(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := newMigrationTestDB(t, ctx, databaseURL)

	originalSQL := `
		SELECT pg_sleep(0.05);
		CREATE TABLE migration_probe (id TEXT PRIMARY KEY);
	`
	original := migrationTestFS(originalSQL)
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			errorsFound <- postgres.Migrate(ctx, db, original)
		}()
	}
	close(start)
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatalf("concurrent Migrate() error = %v", err)
		}
	}

	var appliedCount int
	var checksumLength int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*), length(max(checksum)) FROM schema_migrations
	`).Scan(&appliedCount, &checksumLength); err != nil {
		t.Fatalf("query migration records: %v", err)
	}
	if appliedCount != 1 || checksumLength != 64 {
		t.Fatalf("migration records/count checksum = %d/%d, want 1/64", appliedCount, checksumLength)
	}

	changed := migrationTestFS(`
		CREATE TABLE migration_probe (id TEXT PRIMARY KEY, changed BOOLEAN NOT NULL);
	`)
	if err := postgres.Migrate(ctx, db, changed); !errors.Is(err, postgres.ErrMigrationDrift) {
		t.Fatalf("Migrate() changed applied file error = %v, want migration drift", err)
	}

	renamed := fstest.MapFS{
		"000001_renamed_probe.sql": &fstest.MapFile{Data: []byte(originalSQL)},
	}
	if err := postgres.Migrate(ctx, db, renamed); !errors.Is(err, postgres.ErrMigrationDrift) {
		t.Fatalf("Migrate() renamed applied file error = %v, want migration drift", err)
	}
}

func TestIntegrationMigrateBackfillsLegacyChecksums(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := newMigrationTestDB(t, ctx, databaseURL)

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE migration_probe (id TEXT PRIMARY KEY);
		INSERT INTO schema_migrations (version, name)
		VALUES (1, '000001_create_probe.sql');
	`); err != nil {
		t.Fatalf("create legacy migration state: %v", err)
	}

	migrations := migrationTestFS(`CREATE TABLE migration_probe (id TEXT PRIMARY KEY);`)
	if err := postgres.Migrate(ctx, db, migrations); err != nil {
		t.Fatalf("Migrate() legacy state error = %v", err)
	}

	var checksumLength int
	var checksumNotNull bool
	if err := db.QueryRowContext(ctx, `
		SELECT length(checksum), (
			SELECT attnotnull
			FROM pg_attribute
			WHERE attrelid = 'schema_migrations'::regclass AND attname = 'checksum'
		)
		FROM schema_migrations
		WHERE version = 1
	`).Scan(&checksumLength, &checksumNotNull); err != nil {
		t.Fatalf("query backfilled migration checksum: %v", err)
	}
	if checksumLength != 64 || !checksumNotNull {
		t.Fatalf("legacy checksum length/not-null = %d/%t, want 64/true", checksumLength, checksumNotNull)
	}
}

func migrationTestFS(sql string) fs.FS {
	return fstest.MapFS{
		"000001_create_probe.sql": &fstest.MapFile{Data: []byte(sql)},
	}
}

func newMigrationTestDB(t *testing.T, ctx context.Context, databaseURL string) *sql.DB {
	t.Helper()

	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	adminDB, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create migration test schema: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := adminDB.ExecContext(dropCtx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop migration test schema: %v", err)
		}
	})

	db, err := postgres.Open(ctx, withSearchPath(t, databaseURL, schema))
	if err != nil {
		t.Fatalf("connect to migration test schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
