package inventory_test

import (
	"errors"
	"math"
	"testing"

	"example.com/order-system/internal/domain/inventory"
)

func TestStockReserve(t *testing.T) {
	stock, err := inventory.NewStock("product-1", 5)
	if err != nil {
		t.Fatalf("NewStock() error = %v", err)
	}

	reserved, err := stock.Reserve(2)
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}

	if reserved.Available() != 3 {
		t.Errorf("reserved stock = %d, want 3", reserved.Available())
	}
	if stock.Available() != 5 {
		t.Errorf("original stock = %d, want 5", stock.Available())
	}
}

func TestStockRejectsInvalidReservation(t *testing.T) {
	stock, err := inventory.NewStock("product-1", 1)
	if err != nil {
		t.Fatalf("NewStock() error = %v", err)
	}

	for _, tt := range []struct {
		name     string
		quantity int
		wantErr  error
	}{
		{name: "zero quantity", quantity: 0, wantErr: inventory.ErrInvalidQuantity},
		{name: "negative quantity", quantity: -1, wantErr: inventory.ErrInvalidQuantity},
		{name: "insufficient stock", quantity: 2, wantErr: inventory.ErrInsufficientStock},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := stock.Reserve(tt.quantity)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Reserve() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestStockRelease(t *testing.T) {
	stock, err := inventory.NewStock("product-1", 3)
	if err != nil {
		t.Fatalf("NewStock() error = %v", err)
	}

	released, err := stock.Release(2)
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if released.Available() != 5 {
		t.Fatalf("released stock = %d, want 5", released.Available())
	}
	if stock.Available() != 3 {
		t.Fatalf("original stock = %d, want 3", stock.Available())
	}
}

func TestStockRejectsInvalidRelease(t *testing.T) {
	for _, test := range []struct {
		name      string
		available int
		quantity  int
		wantErr   error
	}{
		{name: "zero quantity", available: 1, quantity: 0, wantErr: inventory.ErrInvalidQuantity},
		{name: "negative quantity", available: 1, quantity: -1, wantErr: inventory.ErrInvalidQuantity},
		{name: "integer overflow", available: math.MaxInt, quantity: 1, wantErr: inventory.ErrStockOverflow},
	} {
		t.Run(test.name, func(t *testing.T) {
			stock, err := inventory.NewStock("product-1", test.available)
			if err != nil {
				t.Fatalf("NewStock() error = %v", err)
			}
			if _, err := stock.Release(test.quantity); !errors.Is(err, test.wantErr) {
				t.Fatalf("Release() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewStockRejectsInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name      string
		productID string
		available int
		wantErr   error
	}{
		{name: "missing product ID", productID: "   ", wantErr: inventory.ErrInvalidProductID},
		{name: "negative stock", productID: "product-1", available: -1, wantErr: inventory.ErrInvalidStock},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := inventory.NewStock(tt.productID, tt.available)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewStock() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
