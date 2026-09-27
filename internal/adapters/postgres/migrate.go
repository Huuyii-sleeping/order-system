package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const migrationLockID int64 = 73487239018217

// Migrate 按文件名顺序执行尚未应用的迁移。迁移锁、SQL 和版本记录
// 位于同一个事务中，多个应用实例同时启动时也只有一个能够执行迁移。
func Migrate(ctx context.Context, db *sql.DB, migrationFS fs.FS) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if err := applyMigration(ctx, tx, migrationFS, name); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}

	return nil
}

func applyMigration(ctx context.Context, tx *sql.Tx, migrationFS fs.FS, name string) error {
	version, err := migrationVersion(name)
	if err != nil {
		return err
	}

	contents, err := fs.ReadFile(migrationFS, name)
	if err != nil {
		return fmt.Errorf("read migration %q: %w", name, err)
	}

	var applied bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM schema_migrations WHERE version = $1
		)
	`, version).Scan(&applied); err != nil {
		return fmt.Errorf("check migration %q: %w", name, err)
	}
	if applied {
		return nil
	}

	if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
		return fmt.Errorf("execute migration %q: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, name) VALUES ($1, $2)
	`, version, name); err != nil {
		return fmt.Errorf("record migration %q: %w", name, err)
	}
	return nil
}

func migrationVersion(name string) (int64, error) {
	base := filepath.Base(name)
	prefix, _, found := strings.Cut(base, "_")
	if !found {
		return 0, fmt.Errorf("migration %q must start with a numeric version and underscore", name)
	}

	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %q has an invalid version", name)
	}

	return version, nil
}
