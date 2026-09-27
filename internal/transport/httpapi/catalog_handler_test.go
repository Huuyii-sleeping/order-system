package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/transport/httpapi"
)

type catalogQueriesStub struct {
	products []catalog.Product
	product  catalog.Product
	err      error
}

func (s catalogQueriesStub) ListProducts(context.Context) ([]catalog.Product, error) {
	return s.products, s.err
}

func (s catalogQueriesStub) GetProduct(context.Context, string) (catalog.Product, error) {
	return s.product, s.err
}

func TestListProducts(t *testing.T) {
	product := mustProduct(t, "product-1", "Keyboard", 12900)
	router := httpapi.NewRouter(catalogQueriesStub{products: []catalog.Product{product}})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/products", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	var body struct {
		Items []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			PriceCents int64  `json:"price_cents"`
		} `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(body.Items) != 1 || body.Items[0].ID != "product-1" || body.Items[0].PriceCents != 12900 {
		t.Fatalf("response items = %+v", body.Items)
	}
}

func TestGetProduct(t *testing.T) {
	product := mustProduct(t, "product-1", "Keyboard", 12900)
	router := httpapi.NewRouter(catalogQueriesStub{product: product})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/products/product-1", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	var body struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		PriceCents int64  `json:"price_cents"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ID != "product-1" || body.Name != "Keyboard" || body.PriceCents != 12900 {
		t.Fatalf("response body = %+v", body)
	}
}

func TestGetProductReturnsNotFound(t *testing.T) {
	router := httpapi.NewRouter(catalogQueriesStub{err: catalog.ErrProductNotFound})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/products/missing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestCatalogHandlerHidesInternalError(t *testing.T) {
	router := httpapi.NewRouter(catalogQueriesStub{err: errors.New("database password leaked")})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/products", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if response.Body.String() != "{\"error\":\"internal server error\"}\n" {
		t.Fatalf("response body = %q", response.Body.String())
	}
}

func mustProduct(t *testing.T, id, name string, priceCents int64) catalog.Product {
	t.Helper()

	product, err := catalog.NewProduct(id, name, priceCents)
	if err != nil {
		t.Fatalf("create product fixture: %v", err)
	}

	return product
}
