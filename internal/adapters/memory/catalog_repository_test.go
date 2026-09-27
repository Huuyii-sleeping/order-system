package memory_test

import (
	"context"
	"errors"
	"testing"

	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/domain/catalog"
)

func TestCatalogRepository(t *testing.T) {
	product, err := catalog.NewProduct("product-1", "Keyboard", 12900)
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	repository := memory.NewCatalogRepository([]catalog.Product{product})

	got, err := repository.GetByID(context.Background(), "product-1")
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.ID() != product.ID() {
		t.Errorf("GetByID() ID = %q, want %q", got.ID(), product.ID())
	}

	products, err := repository.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(products) != 1 || products[0].ID() != product.ID() {
		t.Fatalf("List() = %+v", products)
	}

	// 修改返回切片不能影响仓库内部保存的列表。
	products = append(products, product)
	productsAgain, err := repository.List(context.Background())
	if err != nil {
		t.Fatalf("second List() error = %v", err)
	}
	if len(productsAgain) != 1 {
		t.Fatalf("second List() length = %d, want 1", len(productsAgain))
	}

	_, err = repository.GetByID(context.Background(), "missing")
	if !errors.Is(err, catalog.ErrProductNotFound) {
		t.Fatalf("GetByID() error = %v, want product not found", err)
	}
}

func TestCatalogRepositoryHonorsCanceledContext(t *testing.T) {
	repository := memory.NewCatalogRepository(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repository.List(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("List() error = %v, want context canceled", err)
	}
}
