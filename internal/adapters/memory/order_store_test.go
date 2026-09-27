package memory_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/memory"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

func TestOrderStoreRollsBackFailedTransaction(t *testing.T) {
	store := mustOrderStore(t, 5)
	created := mustStoredOrder(t, "order-1", "key-1")
	wantErr := errors.New("later operation failed")

	err := store.Transact(context.Background(), func(tx orderapp.Transaction) error {
		stock, err := tx.GetStock("product-1")
		if err != nil {
			return err
		}
		stock, err = stock.Reserve(2)
		if err != nil {
			return err
		}
		if err := tx.SaveStock(stock); err != nil {
			return err
		}
		if err := tx.SaveOrder(created); err != nil {
			return err
		}

		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Transact() error = %v, want %v", err, wantErr)
	}

	if available := availableStock(t, store); available != 5 {
		t.Errorf("available stock = %d, want 5 after rollback", available)
	}
	_, err = store.GetOrderByID(context.Background(), created.ID())
	if !errors.Is(err, order.ErrOrderNotFound) {
		t.Fatalf("GetOrderByID() error = %v, want order not found", err)
	}
}

func TestConcurrentOrdersDoNotOversellStock(t *testing.T) {
	store := mustOrderStore(t, 5)
	service := newOrderService(t, store)

	const requestCount = 20
	var successes atomic.Int32
	var insufficient atomic.Int32
	var wg sync.WaitGroup

	for index := range requestCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{
				CustomerID:     "customer-1",
				ProductID:      "product-1",
				Quantity:       1,
				IdempotencyKey: fmt.Sprintf("key-%d", index),
			})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, inventory.ErrInsufficientStock):
				insufficient.Add(1)
			default:
				t.Errorf("CreateOrder() unexpected error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := successes.Load(); got != 5 {
		t.Errorf("successful orders = %d, want 5", got)
	}
	if got := insufficient.Load(); got != requestCount-5 {
		t.Errorf("insufficient stock errors = %d, want %d", got, requestCount-5)
	}
	if available := availableStock(t, store); available != 0 {
		t.Errorf("available stock = %d, want 0", available)
	}
}

func TestConcurrentIdempotentRequestsCreateOneOrder(t *testing.T) {
	store := mustOrderStore(t, 5)
	service := newOrderService(t, store)

	const requestCount = 20
	orderIDs := make(chan string, requestCount)
	errorsFound := make(chan error, requestCount)
	var createdCount atomic.Int32
	var wg sync.WaitGroup

	for range requestCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{
				CustomerID:     "customer-1",
				ProductID:      "product-1",
				Quantity:       2,
				IdempotencyKey: "same-key",
			})
			if err != nil {
				errorsFound <- err
				return
			}
			if result.Created {
				createdCount.Add(1)
			}
			orderIDs <- result.Order.ID()
		}()
	}
	wg.Wait()
	close(orderIDs)
	close(errorsFound)

	for err := range errorsFound {
		t.Errorf("CreateOrder() error = %v", err)
	}
	if got := createdCount.Load(); got != 1 {
		t.Errorf("newly created results = %d, want 1", got)
	}

	var firstID string
	for orderID := range orderIDs {
		if firstID == "" {
			firstID = orderID
		}
		if orderID != firstID {
			t.Errorf("order ID = %q, want all IDs to equal %q", orderID, firstID)
		}
	}
	if available := availableStock(t, store); available != 3 {
		t.Errorf("available stock = %d, want 3", available)
	}
}

type sequenceIDGenerator struct{ next atomic.Uint64 }

func (g *sequenceIDGenerator) NewID() (string, error) {
	return fmt.Sprintf("order-%d", g.next.Add(1)), nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func newOrderService(t *testing.T, store *memory.OrderStore) *orderapp.Service {
	t.Helper()
	product, err := catalog.NewProduct("product-1", "Keyboard", 12900)
	if err != nil {
		t.Fatalf("create product fixture: %v", err)
	}

	return orderapp.NewService(
		memory.NewCatalogRepository([]catalog.Product{product}),
		store,
		&sequenceIDGenerator{},
		fixedClock{now: time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)},
	)
}

func mustOrderStore(t *testing.T, available int) *memory.OrderStore {
	t.Helper()
	stock, err := inventory.NewStock("product-1", available)
	if err != nil {
		t.Fatalf("create stock fixture: %v", err)
	}
	store, err := memory.NewOrderStore([]inventory.Stock{stock})
	if err != nil {
		t.Fatalf("NewOrderStore() error = %v", err)
	}
	return store
}

func availableStock(t *testing.T, store *memory.OrderStore) int {
	t.Helper()
	var available int
	err := store.Transact(context.Background(), func(tx orderapp.Transaction) error {
		stock, err := tx.GetStock("product-1")
		if err != nil {
			return err
		}
		available = stock.Available()
		return nil
	})
	if err != nil {
		t.Fatalf("read stock: %v", err)
	}
	return available
}

func mustStoredOrder(t *testing.T, orderID, idempotencyKey string) order.Order {
	t.Helper()
	created, err := order.New(
		orderID,
		"customer-1",
		"product-1",
		1,
		12900,
		idempotencyKey,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("create order fixture: %v", err)
	}
	return created
}
