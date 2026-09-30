// Package app 负责创建应用依赖并管理外部资源的生命周期。
package app

import (
	"context"
	"fmt"
	"io"

	"example.com/order-system/internal/adapters/clock"
	"example.com/order-system/internal/adapters/idgenerator"
	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/adapters/password"
	"example.com/order-system/internal/adapters/paymentprovider"
	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/adapters/sessiontoken"
	authapp "example.com/order-system/internal/application/auth"
	catalogapp "example.com/order-system/internal/application/catalog"
	orderapp "example.com/order-system/internal/application/order"
	outboxapp "example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/config"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
)

type Services struct {
	Catalog         *catalogapp.Service
	Auth            *authapp.Service
	Orders          *orderapp.Service
	Outboxes        []outboxapp.Store
	Payments        *paymentapp.Consumer
	PaymentResults  *orderapp.PaymentResultConsumer
	PaymentQueries  *paymentapp.QueryService
	PaymentTasks    paymentapp.TaskStore
	PaymentProvider paymentapp.Provider

	readiness []dependencyPinger
	closer    io.Closer
}

type dependencyPinger interface {
	PingContext(context.Context) error
}

func NewServices(ctx context.Context, cfg config.Config) (*Services, error) {
	var products catalogapp.Repository
	var identities authapp.Store
	var orders interface {
		orderapp.Store
		orderapp.PaymentResultStore
	}
	var outboxStores []outboxapp.Store
	var paymentStore interface {
		paymentapp.Store
		paymentapp.TaskStore
		paymentapp.QueryStore
	}
	var readiness []dependencyPinger
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
		identities = memory.NewIdentityStore()
		memoryOrders, err := memory.NewOrderStore(seededStocks)
		if err != nil {
			return nil, fmt.Errorf("create memory order store: %w", err)
		}
		orders = memoryOrders
		memoryPayments := memory.NewPaymentStore()
		paymentStore = memoryPayments
		outboxStores = []outboxapp.Store{memoryOrders, memoryPayments}

	case config.StoragePostgres:
		db, err := postgres.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, err
		}
		closer = db
		readiness = append(readiness, db)
		products = postgres.NewCatalogRepository(db)
		identities = postgres.NewIdentityStore(db)
		orders = postgres.NewOrderStore(db)
		outboxStores = []outboxapp.Store{postgres.NewOutboxStore(db)}
		paymentStore = postgres.NewPaymentStore(db)

	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.StorageDriver)
	}

	paymentConsumer := paymentapp.NewConsumer(
		paymentStore, idgenerator.Random{}, clock.System{},
	)
	paymentResultConsumer := orderapp.NewPaymentResultConsumer(orders, clock.System{})
	return &Services{
		Catalog: catalogapp.NewService(products),
		Auth: authapp.NewService(
			identities,
			password.NewBcrypt(cfg.BcryptCost),
			idgenerator.Random{},
			sessiontoken.Random{},
			clock.System{},
			cfg.AccessTokenTTL,
			cfg.RefreshTokenTTL,
		),
		Orders: orderapp.NewService(
			products,
			orders,
			idgenerator.Random{},
			clock.System{},
		),
		Outboxes:        outboxStores,
		Payments:        paymentConsumer,
		PaymentResults:  paymentResultConsumer,
		PaymentQueries:  paymentapp.NewQueryService(paymentStore),
		PaymentTasks:    paymentStore,
		PaymentProvider: paymentprovider.NewSimulated(),
		readiness:       readiness,
		closer:          closer,
	}, nil
}

// AddReadinessDependency 把启动后新增的外部依赖纳入 /readyz。
// 例如 Kafka 模式下，数据库和 Broker 任一不可用都不应继续接收新业务流量。
func (s *Services) AddReadinessDependency(dependency interface {
	PingContext(context.Context) error
}) {
	if dependency != nil {
		s.readiness = append(s.readiness, dependency)
	}
}

// CheckReadiness 检查当前实例处理业务请求所依赖的外部资源。
// 内存模式没有外部依赖，因此直接就绪；PostgreSQL 模式会实际 Ping 连接池。
func (s *Services) CheckReadiness(ctx context.Context) error {
	for _, dependency := range s.readiness {
		if err := dependency.PingContext(ctx); err != nil {
			return fmt.Errorf("check dependency readiness: %w", err)
		}
	}
	return nil
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
