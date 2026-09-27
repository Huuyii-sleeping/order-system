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
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/migrations"
)

func TestIntegrationPostgresTransactionConcurrencyAndIdempotency(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	schema := fmt.Sprintf("order_system_test_%d", time.Now().UnixNano())
	adminDB, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })

	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := adminDB.ExecContext(dropCtx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})

	testDatabaseURL := withSearchPath(t, databaseURL, schema)
	db, err := postgres.Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect to test schema: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	peerDB, err := postgres.Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect second service instance to test schema: %v", err)
	}
	t.Cleanup(func() { _ = peerDB.Close() })

	if err := postgres.Migrate(ctx, db, migrations.Files); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	// 再执行一次，证明迁移根据 schema_migrations 跳过已应用版本。
	if err := postgres.Migrate(ctx, db, migrations.Files); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	t.Run("rolls back stock change when transaction fails", func(t *testing.T) {
		store := postgres.NewOrderStore(db)
		wantErr := errors.New("stop transaction")
		err := store.Transact(ctx, func(tx orderapp.Transaction) error {
			stock, err := tx.GetStock("product-1")
			if err != nil {
				return err
			}
			reserved, err := stock.Reserve(1)
			if err != nil {
				return err
			}
			if err := tx.SaveStock(reserved); err != nil {
				return err
			}
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("Transact() error = %v, want %v", err, wantErr)
		}
		if got := queryAvailable(t, db, "product-1"); got != 10 {
			t.Errorf("available = %d, want 10 after rollback", got)
		}
	})

	t.Run("prevents overselling across service instances", func(t *testing.T) {
		services := []*orderapp.Service{
			newPostgresOrderService(db, &sequenceIDGenerator{prefix: "oversell-a"}),
			newPostgresOrderService(peerDB, &sequenceIDGenerator{prefix: "oversell-b"}),
		}

		const requests = 20
		var succeeded atomic.Int32
		var insufficient atomic.Int32
		var waitGroup sync.WaitGroup
		for index := range requests {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				_, err := services[index%len(services)].CreateOrder(ctx, orderapp.CreateOrderCommand{
					CustomerID:     "oversell-customer",
					ProductID:      "product-1",
					Quantity:       1,
					IdempotencyKey: fmt.Sprintf("oversell-key-%d", index),
				})
				switch {
				case err == nil:
					succeeded.Add(1)
				case errors.Is(err, inventory.ErrInsufficientStock):
					insufficient.Add(1)
				default:
					t.Errorf("CreateOrder() unexpected error = %v", err)
				}
			}()
		}
		waitGroup.Wait()

		if got := succeeded.Load(); got != 10 {
			t.Errorf("successful orders = %d, want 10", got)
		}
		if got := insufficient.Load(); got != 10 {
			t.Errorf("insufficient errors = %d, want 10", got)
		}
		if got := queryAvailable(t, db, "product-1"); got != 0 {
			t.Errorf("available = %d, want 0", got)
		}
	})

	t.Run("deduplicates concurrent requests across service instances", func(t *testing.T) {
		services := []*orderapp.Service{
			newPostgresOrderService(db, &sequenceIDGenerator{prefix: "idempotent-a"}),
			newPostgresOrderService(peerDB, &sequenceIDGenerator{prefix: "idempotent-b"}),
		}

		const requests = 20
		var created atomic.Int32
		orderIDs := make(chan string, requests)
		errorsFound := make(chan error, requests)
		var waitGroup sync.WaitGroup
		for index := range requests {
			waitGroup.Add(1)
			go func() {
				defer waitGroup.Done()
				result, err := services[index%len(services)].CreateOrder(ctx, orderapp.CreateOrderCommand{
					CustomerID:     "idempotent-customer",
					ProductID:      "product-2",
					Quantity:       2,
					IdempotencyKey: "shared-key",
				})
				if err != nil {
					errorsFound <- err
					return
				}
				if result.Created {
					created.Add(1)
				}
				orderIDs <- result.Order.ID()
			}()
		}
		waitGroup.Wait()
		close(orderIDs)
		close(errorsFound)

		for err := range errorsFound {
			t.Errorf("CreateOrder() error = %v", err)
		}
		if got := created.Load(); got != 1 {
			t.Errorf("new orders = %d, want 1", got)
		}

		var firstID string
		for orderID := range orderIDs {
			if firstID == "" {
				firstID = orderID
			}
			if orderID != firstID {
				t.Errorf("order ID = %q, want %q", orderID, firstID)
			}
		}
		if got := queryAvailable(t, db, "product-2"); got != 18 {
			t.Errorf("available = %d, want 18", got)
		}
	})
}

type sequenceIDGenerator struct {
	prefix string
	next   atomic.Uint64
}

func (g *sequenceIDGenerator) NewID() (string, error) {
	return fmt.Sprintf("%s-order-%d", g.prefix, g.next.Add(1)), nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
}

func newPostgresOrderService(db *sql.DB, ids orderapp.IDGenerator) *orderapp.Service {
	return orderapp.NewService(
		postgres.NewCatalogRepository(db),
		postgres.NewOrderStore(db),
		ids,
		fixedClock{},
	)
}

func queryAvailable(t *testing.T, db *sql.DB, productID string) int {
	t.Helper()
	var available int
	if err := db.QueryRow(`SELECT available FROM stocks WHERE product_id = $1`, productID).Scan(&available); err != nil {
		t.Fatalf("query stock: %v", err)
	}
	return available
}

func withSearchPath(t *testing.T, databaseURL, schema string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
