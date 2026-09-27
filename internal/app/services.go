// Package app 负责创建应用依赖并管理外部资源的生命周期。
package app

import (
	"context"
	"fmt"
	"io"

	"example.com/order-system/internal/adapters/clock"
	"example.com/order-system/internal/adapters/idgenerator"
	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/adapters/postgres"
	catalogapp "example.com/order-system/internal/application/catalog"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/config"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
)

type Services struct {
	Catalog *catalogapp.Service
	Orders  *orderapp.Service

	closer io.Closer
}

func NewServices(ctx context.Context, cfg config.Config) (*Services, error) {
	var products catalogapp.Repository
	var orders orderapp.Store
	var closer io.Closer

	switch cfg.StorageDriver {
	case config.StorageMemory:
		seededProducts, err := seedProducts()
		if err != nil {
			return nil, fmt.Errorf("seed products: %w", err)
		}
		seededStocks, err := seedStocks()
		if err != nil {
			return nil, fmt.Errorf("seed stocks: %w", err)
		}

		products = memory.NewCatalogRepository(seededProducts)
		orders, err = memory.NewOrderStore(seededStocks)
		if err != nil {
			return nil, fmt.Errorf("create memory order store: %w", err)
		}

	case config.StoragePostgres:
		db, err := postgres.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		closer = db
		products = postgres.NewCatalogRepository(db)
		orders = postgres.NewOrderStore(db)

	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.StorageDriver)
	}

	return &Services{
		Catalog: catalogapp.NewService(products),
		Orders: orderapp.NewService(
			products,
			orders,
			idgenerator.Random{},
			clock.System{},
		),
		closer: closer,
	}, nil
}

func (s *Services) Close() error {
	if s.closer == nil {
		return nil
	}
	return s.closer.Close()
}

func seedProducts() ([]catalog.Product, error) {
	definitions := []struct {
		id         string
		name       string
		priceCents int64
	}{
		{id: "product-1", name: "Mechanical Keyboard", priceCents: 12900},
		{id: "product-2", name: "Wireless Mouse", priceCents: 6900},
	}

	products := make([]catalog.Product, 0, len(definitions))
	for _, definition := range definitions {
		product, err := catalog.NewProduct(definition.id, definition.name, definition.priceCents)
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}

	return products, nil
}

func seedStocks() ([]inventory.Stock, error) {
	definitions := []struct {
		productID string
		available int
	}{
		{productID: "product-1", available: 10},
		{productID: "product-2", available: 20},
	}

	stocks := make([]inventory.Stock, 0, len(definitions))
	for _, definition := range definitions {
		stock, err := inventory.NewStock(definition.productID, definition.available)
		if err != nil {
			return nil, err
		}
		stocks = append(stocks, stock)
	}

	return stocks, nil
}
