// Package postgres 提供基于 PostgreSQL 的持久化适配器。
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PoolOptions 是单个应用实例可以占用的数据库连接预算。
// 多实例部署时，数据库看到的理论最大连接数约为 MaxOpenConns × 实例数。
type PoolOptions struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func DefaultPoolOptions() PoolOptions {
	return PoolOptions{
		MaxOpenConns: 20, MaxIdleConns: 5,
		ConnMaxLifetime: 30 * time.Minute, ConnMaxIdleTime: 5 * time.Minute,
	}
}

// Open 创建并验证 PostgreSQL 连接池。sql.DB 本身是并发安全的连接池，
// 不代表某一条固定连接。具体事务会从池中独占一条连接直到提交或回滚。
func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	return OpenWithOptions(ctx, databaseURL, DefaultPoolOptions())
}

func OpenWithOptions(
	ctx context.Context,
	databaseURL string,
	options PoolOptions,
) (*sql.DB, error) {
	if err := options.validate(); err != nil {
		return nil, fmt.Errorf("validate postgres pool options: %w", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	db.SetMaxOpenConns(options.MaxOpenConns)
	db.SetMaxIdleConns(options.MaxIdleConns)
	db.SetConnMaxLifetime(options.ConnMaxLifetime)
	db.SetConnMaxIdleTime(options.ConnMaxIdleTime)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return db, nil
}

func (o PoolOptions) validate() error {
	switch {
	case o.MaxOpenConns <= 0:
		return errors.New("max open connections must be greater than zero")
	case o.MaxIdleConns <= 0:
		return errors.New("max idle connections must be greater than zero")
	case o.MaxIdleConns > o.MaxOpenConns:
		return errors.New("max idle connections must not exceed max open connections")
	case o.ConnMaxLifetime <= 0:
		return errors.New("connection max lifetime must be greater than zero")
	case o.ConnMaxIdleTime <= 0:
		return errors.New("connection max idle time must be greater than zero")
	default:
		return nil
	}
}
