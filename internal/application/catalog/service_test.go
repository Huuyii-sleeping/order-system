package catalogapp_test

import (
	"context"
	"errors"
	"testing"

	catalogapp "example.com/order-system/internal/application/catalog"
	"example.com/order-system/internal/domain/catalog"
)

type repositoryStub struct {
	products []catalog.Product
	product  catalog.Product
	err      error
}

func (r repositoryStub) List(context.Context) ([]catalog.Product, error) {
	return r.products, r.err
}

func (r repositoryStub) GetByID(context.Context, string) (catalog.Product, error) {
	return r.product, r.err
}

func TestServicePreservesRepositoryErrorIdentity(t *testing.T) {
	service := catalogapp.NewService(repositoryStub{err: catalog.ErrProductNotFound})

	_, err := service.GetProduct(context.Background(), "missing")
	if !errors.Is(err, catalog.ErrProductNotFound) {
		t.Fatalf("GetProduct() error = %v, want product not found", err)
	}
}

func TestServiceReturnsProducts(t *testing.T) {
	product, err := catalog.NewProduct("product-1", "Keyboard", 12900)
	if err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	service := catalogapp.NewService(repositoryStub{
		products: []catalog.Product{product},
		product:  product,
	})

	products, err := service.ListProducts(context.Background())
	if err != nil {
		t.Fatalf("ListProducts() error = %v", err)
	}
	if len(products) != 1 || products[0].ID() != product.ID() {
		t.Fatalf("ListProducts() = %+v", products)
	}

	got, err := service.GetProduct(context.Background(), product.ID())
	if err != nil {
		t.Fatalf("GetProduct() error = %v", err)
	}
	if got.ID() != product.ID() {
		t.Errorf("GetProduct() ID = %q, want %q", got.ID(), product.ID())
	}
}
