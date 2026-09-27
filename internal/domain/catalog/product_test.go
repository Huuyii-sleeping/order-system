package catalog_test

import (
	"errors"
	"testing"

	"example.com/order-system/internal/domain/catalog"
)

func TestNewProduct(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		productName string
		priceCents  int64
		wantErr     error
	}{
		{name: "valid product", id: "product-1", productName: "Keyboard", priceCents: 12900},
		{name: "missing ID", id: "  ", productName: "Keyboard", priceCents: 12900, wantErr: catalog.ErrInvalidProductID},
		{name: "missing name", id: "product-1", productName: "  ", priceCents: 12900, wantErr: catalog.ErrInvalidProductName},
		{name: "zero price", id: "product-1", productName: "Keyboard", priceCents: 0, wantErr: catalog.ErrInvalidProductPrice},
		{name: "negative price", id: "product-1", productName: "Keyboard", priceCents: -1, wantErr: catalog.ErrInvalidProductPrice},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product, err := catalog.NewProduct(tt.id, tt.productName, tt.priceCents)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewProduct() error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				return
			}

			if product.ID() != tt.id || product.Name() != tt.productName || product.PriceCents() != tt.priceCents {
				t.Errorf("NewProduct() = {%q %q %d}", product.ID(), product.Name(), product.PriceCents())
			}
		})
	}
}

func TestNewProductTrimsTextFields(t *testing.T) {
	product, err := catalog.NewProduct("  product-1  ", "  Keyboard  ", 12900)
	if err != nil {
		t.Fatalf("NewProduct() error = %v", err)
	}

	if product.ID() != "product-1" {
		t.Errorf("ID = %q, want %q", product.ID(), "product-1")
	}
	if product.Name() != "Keyboard" {
		t.Errorf("Name = %q, want %q", product.Name(), "Keyboard")
	}
}
