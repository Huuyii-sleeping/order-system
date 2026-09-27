package order_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"example.com/order-system/internal/domain/order"
)

func TestNewOrder(t *testing.T) {
	createdAt := time.Date(2026, 9, 27, 8, 0, 0, 0, time.FixedZone("test", 8*60*60))

	created, err := order.New("order-1", "customer-1", "product-1", 2, 12900, "key-1", createdAt)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if created.TotalCents() != 25800 {
		t.Errorf("TotalCents() = %d, want 25800", created.TotalCents())
	}
	if created.Status() != order.StatusPending {
		t.Errorf("Status() = %q, want %q", created.Status(), order.StatusPending)
	}
	if created.CreatedAt().Location() != time.UTC {
		t.Errorf("CreatedAt() location = %v, want UTC", created.CreatedAt().Location())
	}
}

func TestNewOrderRejectsInvalidInput(t *testing.T) {
	now := time.Now()

	for _, tt := range []struct {
		name           string
		id             string
		customerID     string
		productID      string
		quantity       int
		unitPriceCents int64
		idempotencyKey string
		createdAt      time.Time
		wantErr        error
	}{
		{name: "missing ID", customerID: "c1", productID: "p1", quantity: 1, unitPriceCents: 1, idempotencyKey: "key", createdAt: now, wantErr: order.ErrInvalidOrderID},
		{name: "missing customer", id: "o1", productID: "p1", quantity: 1, unitPriceCents: 1, idempotencyKey: "key", createdAt: now, wantErr: order.ErrInvalidCustomerID},
		{name: "missing product", id: "o1", customerID: "c1", quantity: 1, unitPriceCents: 1, idempotencyKey: "key", createdAt: now, wantErr: order.ErrInvalidProductID},
		{name: "invalid quantity", id: "o1", customerID: "c1", productID: "p1", quantity: 0, unitPriceCents: 1, idempotencyKey: "key", createdAt: now, wantErr: order.ErrInvalidQuantity},
		{name: "invalid unit price", id: "o1", customerID: "c1", productID: "p1", quantity: 1, unitPriceCents: 0, idempotencyKey: "key", createdAt: now, wantErr: order.ErrInvalidUnitPrice},
		{name: "missing idempotency key", id: "o1", customerID: "c1", productID: "p1", quantity: 1, unitPriceCents: 1, createdAt: now, wantErr: order.ErrInvalidIdempotencyKey},
		{name: "missing creation time", id: "o1", customerID: "c1", productID: "p1", quantity: 1, unitPriceCents: 1, idempotencyKey: "key", wantErr: order.ErrInvalidCreatedAt},
		{name: "overflow", id: "o1", customerID: "c1", productID: "p1", quantity: 2, unitPriceCents: math.MaxInt64, idempotencyKey: "key", createdAt: now, wantErr: order.ErrOrderTotalOverflow},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := order.New(tt.id, tt.customerID, tt.productID, tt.quantity, tt.unitPriceCents, tt.idempotencyKey, tt.createdAt)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
