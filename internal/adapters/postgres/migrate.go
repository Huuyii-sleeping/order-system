package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const migrationLockID int64 = 73487239018217

var ErrMigrationDrift = errors.New("applied migration differs from embedded migration")

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
			checksum TEXT,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`); err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	// 老版本 schema_migrations 没有 checksum。先以可空列完成兼容升级，
	// applyMigration 会用当前嵌入文件为历史版本建立基线。
	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT
	`); err != nil {
		return fmt.Errorf("add migration checksum: %w", err)
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
	if _, err := tx.ExecContext(ctx, `
		ALTER TABLE schema_migrations ALTER COLUMN checksum SET NOT NULL
	`); err != nil {
		return fmt.Errorf("require migration checksums: %w", err)
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
	checksum := migrationChecksum(contents)

	var appliedName string
	var appliedChecksum sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT name, checksum FROM schema_migrations WHERE version = $1
	`, version).Scan(&appliedName, &appliedChecksum)
	switch {
	case err == nil:
		if appliedName != name {
			return fmt.Errorf(
				"%w: version %d is recorded as %q, embedded file is %q",
				ErrMigrationDrift, version, appliedName, name,
			)
		}
		if !appliedChecksum.Valid {
			if _, err := tx.ExecContext(ctx, `
				UPDATE schema_migrations SET checksum = $2 WHERE version = $1
			`, version, checksum); err != nil {
				return fmt.Errorf("backfill migration checksum %q: %w", name, err)
			}
			return nil
		}
		if appliedChecksum.String != checksum {
			return fmt.Errorf(
				"%w: checksum mismatch for version %d file %q",
				ErrMigrationDrift, version, name,
			)
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		// 尚未应用，继续执行 SQL 并记录不可变身份。
	default:
		return fmt.Errorf("check migration %q: %w", name, err)
	}

	if _, err := tx.ExecContext(ctx, string(contents)); err != nil {
		return fmt.Errorf("execute migration %q: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)
	`, version, name, checksum); err != nil {
		return fmt.Errorf("record migration %q: %w", name, err)
	}
	return nil
}

func migrationChecksum(contents []byte) string {
	sum := sha256.Sum256(contents)
	return fmt.Sprintf("%x", sum)
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
