package main

import (
	"context"
	"os"
	"time"

	"example.com/order-system/internal/adapters/postgres"
	platformlogging "example.com/order-system/internal/platform/logging"
	"example.com/order-system/migrations"
)

func main() {
	bootstrapLogger := platformlogging.NewDefault(os.Stdout, "order-system-migrate")
	logger, err := platformlogging.New(os.Stdout, platformlogging.Config{
		ServiceName: "order-system-migrate",
		Level:       os.Getenv("LOG_LEVEL"),
	})
	if err != nil {
		bootstrapLogger.Error("initialize logging", "error", err)
		os.Exit(1)
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		logger.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		logger.Error("connect to postgres failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := postgres.Migrate(ctx, db, migrations.Files); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}

	logger.Info("database migrations completed")
}
