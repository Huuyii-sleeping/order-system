package orderapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

type productCatalogStub struct {
	product catalog.Product
	err     error
}

func (s productCatalogStub) GetByID(context.Context, string) (catalog.Product, error) {
	return s.product, s.err
}

type storeStub struct {
	tx         *transactionStub
	order      order.Order
	err        error
	transact   bool
	orderID    string
	customerID string
}

func (s *storeStub) Transact(ctx context.Context, fn func(orderapp.Transaction) error) error {
	s.transact = true
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.err != nil {
		return s.err
	}

	return fn(s.tx)
}

func (s *storeStub) GetOrderByIDAndCustomerID(_ context.Context, orderID, customerID string) (order.Order, error) {
	s.orderID = orderID
	s.customerID = customerID
	return s.order, s.err
}

type transactionStub struct {
	existing         order.Order
	existingErr      error
	stock            inventory.Stock
	stockErr         error
	saveOrderErr     error
	saveOutboxErr    error
	savedOrder       order.Order
	savedMessage     outbox.Message
	savedStock       inventory.Stock
	saveStockCalled  bool
	saveOrderCalled  bool
	saveOutboxCalled bool
}

func (t *transactionStub) GetOrderByIdempotencyKey(string, string) (order.Order, error) {
	return t.existing, t.existingErr
}

func (t *transactionStub) GetStock(string) (inventory.Stock, error) {
	return t.stock, t.stockErr
}

func (t *transactionStub) SaveStock(stock inventory.Stock) error {
	t.saveStockCalled = true
	t.savedStock = stock
	return nil
}

func (t *transactionStub) SaveOrder(created order.Order) error {
	t.saveOrderCalled = true
	t.savedOrder = created
	return t.saveOrderErr
}

func (t *transactionStub) SaveOutboxMessage(message outbox.Message) error {
	t.saveOutboxCalled = true
	t.savedMessage = message
	return t.saveOutboxErr
}

type idGeneratorStub struct {
	id  string
	err error
}

func (s idGeneratorStub) NewID() (string, error) { return s.id, s.err }

type clockStub struct{ now time.Time }

func (s clockStub) Now() time.Time { return s.now }

func TestCreateOrderReservesStockAndSavesPriceSnapshot(t *testing.T) {
	product := mustProduct(t, 12900)
	stock := mustStock(t, 5)
	tx := &transactionStub{stock: stock, existingErr: order.ErrOrderNotFound}
	store := &storeStub{tx: tx}
	createdAt := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	service := orderapp.NewService(
		productCatalogStub{product: product},
		store,
		idGeneratorStub{id: "order-1"},
		clockStub{now: createdAt},
	)

	result, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{
		CustomerID:     "customer-1",
		ProductID:      "product-1",
		Quantity:       2,
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if !result.Created {
		t.Error("Created = false, want true")
	}
	if result.Order.ID() != "order-1" || result.Order.TotalCents() != 25800 {
		t.Fatalf("created order = %+v", result.Order)
	}
	if !tx.saveStockCalled || tx.savedStock.Available() != 3 {
		t.Fatalf("saved stock = %+v, called = %v", tx.savedStock, tx.saveStockCalled)
	}
	if !tx.saveOrderCalled || tx.savedOrder.ID() != "order-1" {
		t.Fatalf("saved order = %+v, called = %v", tx.savedOrder, tx.saveOrderCalled)
	}
	if !tx.saveOutboxCalled || tx.savedMessage.Topic != orderapp.OrderCreatedTopic ||
		tx.savedMessage.Key != "order-1" {
		t.Fatalf("saved outbox message = %+v, called = %v", tx.savedMessage, tx.saveOutboxCalled)
	}
	var eventPayload struct {
		OrderID    string `json:"order_id"`
		CustomerID string `json:"customer_id"`
		TotalCents int64  `json:"total_cents"`
	}
	if err := json.Unmarshal(tx.savedMessage.Payload, &eventPayload); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	if eventPayload.OrderID != "order-1" || eventPayload.CustomerID != "customer-1" ||
		eventPayload.TotalCents != 25800 {
		t.Errorf("outbox payload = %+v", eventPayload)
	}
}

func TestCreateOrderReturnsExistingOrderForSameIdempotentRequest(t *testing.T) {
	existing := mustOrder(t, "key-1", 2)
	tx := &transactionStub{existing: existing}
	store := &storeStub{tx: tx}
	service := orderapp.NewService(
		productCatalogStub{product: mustProduct(t, 12900)},
		store,
		idGeneratorStub{id: "must-not-be-used"},
		clockStub{now: time.Now()},
	)

	result, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{
		CustomerID:     "customer-1",
		ProductID:      "product-1",
		Quantity:       2,
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	if result.Created {
		t.Error("Created = true, want false")
	}
	if result.Order.ID() != existing.ID() {
		t.Errorf("order ID = %q, want %q", result.Order.ID(), existing.ID())
	}
	if tx.saveStockCalled || tx.saveOrderCalled || tx.saveOutboxCalled {
		t.Fatal("idempotent retry must not write stock, order, or outbox event")
	}
}

func TestCreateOrderRejectsIdempotencyKeyReusedForDifferentRequest(t *testing.T) {
	existing := mustOrder(t, "key-1", 1)
	tx := &transactionStub{existing: existing}
	service := orderapp.NewService(
		productCatalogStub{product: mustProduct(t, 12900)},
		&storeStub{tx: tx},
		idGeneratorStub{id: "order-2"},
		clockStub{now: time.Now()},
	)

	_, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{
		CustomerID:     "customer-1",
		ProductID:      "product-1",
		Quantity:       2,
		IdempotencyKey: "key-1",
	})
	if !errors.Is(err, orderapp.ErrIdempotencyConflict) {
		t.Fatalf("CreateOrder() error = %v, want idempotency conflict", err)
	}
}

func TestCreateOrderStopsWhenStockIsInsufficient(t *testing.T) {
	tx := &transactionStub{stock: mustStock(t, 1), existingErr: order.ErrOrderNotFound}
	service := orderapp.NewService(
		productCatalogStub{product: mustProduct(t, 12900)},
		&storeStub{tx: tx},
		idGeneratorStub{id: "order-1"},
		clockStub{now: time.Now()},
	)

	_, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{
		CustomerID:     "customer-1",
		ProductID:      "product-1",
		Quantity:       2,
		IdempotencyKey: "key-1",
	})
	if !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Fatalf("CreateOrder() error = %v, want insufficient stock", err)
	}
	if tx.saveStockCalled || tx.saveOrderCalled || tx.saveOutboxCalled {
		t.Fatal("failed reservation must not write stock, order, or outbox event")
	}
}

func TestCreateOrderFailsTransactionWhenOutboxWriteFails(t *testing.T) {
	wantErr := errors.New("outbox unavailable")
	tx := &transactionStub{
		stock: mustStock(t, 5), existingErr: order.ErrOrderNotFound, saveOutboxErr: wantErr,
	}
	service := orderapp.NewService(
		productCatalogStub{product: mustProduct(t, 12900)},
		&storeStub{tx: tx}, idGeneratorStub{id: "generated-id"}, clockStub{now: time.Now()},
	)

	_, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{
		CustomerID: "customer-1", ProductID: "product-1", Quantity: 1, IdempotencyKey: "key-1",
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("CreateOrder() error = %v, want %v", err, wantErr)
	}
}

func TestCreateOrderValidatesBeforeCallingDependencies(t *testing.T) {
	store := &storeStub{}
	service := orderapp.NewService(productCatalogStub{}, store, idGeneratorStub{}, clockStub{})

	_, err := service.CreateOrder(context.Background(), orderapp.CreateOrderCommand{})
	if !errors.Is(err, order.ErrInvalidCustomerID) {
		t.Fatalf("CreateOrder() error = %v, want invalid customer ID", err)
	}
	if store.transact {
		t.Fatal("invalid command must not start a transaction")
	}
}

func TestGetOrderScopesQueryToAuthenticatedCustomer(t *testing.T) {
	store := &storeStub{order: mustOrder(t, "key-1", 1)}
	service := orderapp.NewService(productCatalogStub{}, store, idGeneratorStub{}, clockStub{})

	got, err := service.GetOrder(context.Background(), " customer-1 ", " order-existing ")
	if err != nil {
		t.Fatalf("GetOrder() error = %v", err)
	}
	if got.ID() != "order-existing" {
		t.Errorf("order ID = %q", got.ID())
	}
	if store.customerID != "customer-1" || store.orderID != "order-existing" {
		t.Errorf("store query = customer %q, order %q", store.customerID, store.orderID)
	}
}

func mustProduct(t *testing.T, priceCents int64) catalog.Product {
	t.Helper()
	product, err := catalog.NewProduct("product-1", "Keyboard", priceCents)
	if err != nil {
		t.Fatalf("create product fixture: %v", err)
	}
	return product
}

func mustStock(t *testing.T, available int) inventory.Stock {
	t.Helper()
	stock, err := inventory.NewStock("product-1", available)
	if err != nil {
		t.Fatalf("create stock fixture: %v", err)
	}
	return stock
}

func mustOrder(t *testing.T, idempotencyKey string, quantity int) order.Order {
	t.Helper()
	created, err := order.New(
		"order-existing",
		"customer-1",
		"product-1",
		quantity,
		12900,
		idempotencyKey,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("create order fixture: %v", err)
	}
	return created
}
