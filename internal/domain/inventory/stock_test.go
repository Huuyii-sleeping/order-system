package inventory_test

import (
	"errors"
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
