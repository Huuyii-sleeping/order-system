package postgres_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/postgres"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
)

type flakyPublisher struct {
	mu           sync.Mutex
	failuresLeft int
	publishedIDs []string
}

func (p *flakyPublisher) Publish(_ context.Context, message outbox.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failuresLeft > 0 {
		p.failuresLeft--
		return errors.New("simulated broker outage")
	}
	p.publishedIDs = append(p.publishedIDs, message.ID)
	return nil
}

func TestIntegrationTransactionalOutbox(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "outbox-customer")

	ids := &sequenceIDGenerator{prefix: "outbox"}
	service := newPostgresOrderService(db, ids)
	command := orderapp.CreateOrderCommand{
		CustomerID: "outbox-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "outbox-key-1",
	}
	first, err := service.CreateOrder(ctx, command)
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	second, err := service.CreateOrder(ctx, command)
	if err != nil {
		t.Fatalf("idempotent CreateOrder() error = %v", err)
	}
	if !first.Created || second.Created || first.Order.ID() != second.Order.ID() {
		t.Fatalf("create results = %+v / %+v", first, second)
	}

	var orders int
	var messages int
	if err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM orders WHERE customer_id = $1),
			(SELECT count(*) FROM outbox_messages WHERE message_key = $2)
	`, "outbox-customer", first.Order.ID()).Scan(&orders, &messages); err != nil {
		t.Fatalf("count order and outbox rows: %v", err)
	}
	if orders != 1 || messages != 1 {
		t.Fatalf("orders = %d, outbox messages = %d; want 1/1", orders, messages)
	}

	store := postgres.NewOutboxStore(db)
	publisher := &flakyPublisher{failuresLeft: 1}
	worker := outbox.NewWorker(store, publisher, nil, outbox.WorkerConfig{
		BatchSize: 10, LeaseDuration: time.Second,
		RetryBase: 10 * time.Millisecond, RetryMax: time.Second,
	})
	if processed, err := worker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("first RunOnce() = %d, %v; want 1, nil", processed, err)
	}

	var attempts int
	var published bool
	if err := db.QueryRowContext(ctx, `
		SELECT attempts, published_at IS NOT NULL
		FROM outbox_messages WHERE message_key = $1
	`, first.Order.ID()).Scan(&attempts, &published); err != nil {
		t.Fatalf("query failed outbox attempt: %v", err)
	}
	if attempts != 1 || published {
		t.Fatalf("after failure attempts = %d, published = %v", attempts, published)
	}

	time.Sleep(15 * time.Millisecond)
	if processed, err := worker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("second RunOnce() = %d, %v; want 1, nil", processed, err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT attempts, published_at IS NOT NULL
		FROM outbox_messages WHERE message_key = $1
	`, first.Order.ID()).Scan(&attempts, &published); err != nil {
		t.Fatalf("query published outbox attempt: %v", err)
	}
	if attempts != 2 || !published || len(publisher.publishedIDs) != 1 {
		t.Fatalf("after recovery attempts = %d, published = %v, sends = %v",
			attempts, published, publisher.publishedIDs)
	}

	createSecondOutboxOrder(t, ctx, service)
	assertSingleConcurrentClaim(t, ctx, store)
}

func integrationDatabaseURL(t *testing.T) string {
	t.Helper()
	// 与其他 PostgreSQL 集成测试保持同一显式开关，默认测试不会依赖 Docker。
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	return databaseURL
}

func createSecondOutboxOrder(
	t *testing.T, ctx context.Context, service *orderapp.Service,
) {
	t.Helper()
	if _, err := service.CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "outbox-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "outbox-key-2",
	}); err != nil {
		t.Fatalf("create order for concurrent claim: %v", err)
	}
}

func assertSingleConcurrentClaim(
	t *testing.T, ctx context.Context, store *postgres.OutboxStore,
) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan []outbox.Message, 2)
	errorsFound := make(chan error, 2)
	var waitGroup sync.WaitGroup
	now := time.Now().UTC()
	for _, leaseID := range []string{"lease-a", "lease-b"} {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			messages, err := store.Claim(ctx, leaseID, 1, now, now.Add(time.Minute))
			if err != nil {
				errorsFound <- err
				return
			}
			results <- messages
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent Claim() error = %v", err)
	}
	claimed := 0
	for messages := range results {
		claimed += len(messages)
	}
	if claimed != 1 {
		t.Errorf("concurrently claimed messages = %d, want 1", claimed)
	}
}
