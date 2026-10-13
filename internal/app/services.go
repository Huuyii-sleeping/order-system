// Package app 负责创建应用依赖并管理外部资源的生命周期。
package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"example.com/order-system/internal/adapters/clock"
	"example.com/order-system/internal/adapters/idgenerator"
	"example.com/order-system/internal/adapters/memory"
	"example.com/order-system/internal/adapters/notificationprovider"
	"example.com/order-system/internal/adapters/password"
	"example.com/order-system/internal/adapters/paymentprovider"
	"example.com/order-system/internal/adapters/postgres"
	redisadapter "example.com/order-system/internal/adapters/redis"
	"example.com/order-system/internal/adapters/sessiontoken"
	authapp "example.com/order-system/internal/application/auth"
	catalogapp "example.com/order-system/internal/application/catalog"
	notificationapp "example.com/order-system/internal/application/notification"
	orderapp "example.com/order-system/internal/application/order"
	outboxapp "example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/config"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/platform/ratelimit"
)

type Services struct {
	Catalog              *catalogapp.Service
	Auth                 *authapp.Service
	Orders               *orderapp.Service
	Outboxes             []outboxapp.Store
	Payments             *paymentapp.Consumer
	PaymentResults       *orderapp.PaymentResultConsumer
	PaymentQueries       *paymentapp.QueryService
	PaymentTasks         paymentapp.TaskStore
	PaymentProvider      paymentapp.Provider
	Notifications        *notificationapp.Consumer
	NotificationQueries  *notificationapp.QueryService
	NotificationWebhooks *notificationapp.WebhookService
	NotificationTasks    notificationapp.TaskStore
	NotificationProvider notificationapp.Provider
	LoginLimiter         ratelimit.Limiter
	RefreshLimiter       ratelimit.Limiter

	readiness []dependencyPinger
	closers   []io.Closer
	draining  atomic.Bool
}

var ErrDraining = errors.New("service is draining")

type dependencyPinger interface {
	PingContext(context.Context) error
}

type cacheMetrics interface {
	RecordCatalogCacheOperation(string)
}

type serviceMetrics interface {
	cacheMetrics
	paymentprovider.ResilienceMetrics
	RegisterDatabasePool(*sql.DB) error
}

func NewServices(ctx context.Context, cfg config.Config, metrics serviceMetrics) (*Services, error) {
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
	var notificationStore interface {
		notificationapp.Store
		notificationapp.TaskStore
		notificationapp.QueryStore
		notificationapp.WebhookStore
	}
	var readiness []dependencyPinger
	var closers []io.Closer

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
		notificationStore = memory.NewNotificationStore()
		outboxStores = []outboxapp.Store{memoryOrders, memoryPayments}

	case config.StoragePostgres:
		db, err := postgres.OpenWithOptions(ctx, cfg.DatabaseURL, postgres.PoolOptions{
			MaxOpenConns: cfg.DatabaseMaxOpenConns, MaxIdleConns: cfg.DatabaseMaxIdleConns,
			ConnMaxLifetime: cfg.DatabaseConnMaxLifetime, ConnMaxIdleTime: cfg.DatabaseConnMaxIdleTime,
		})
		if err != nil {
			return nil, err
		}
		if metrics != nil {
			if err := metrics.RegisterDatabasePool(db); err != nil {
				_ = db.Close()
				return nil, fmt.Errorf("register database pool metrics: %w", err)
			}
		}
		closers = append(closers, db)
		readiness = append(readiness, db)
		products = postgres.NewCatalogRepository(db)
		identities = postgres.NewIdentityStore(db)
		orders = postgres.NewOrderStore(db)
		outboxStores = []outboxapp.Store{postgres.NewOutboxStore(db)}
		paymentStore = postgres.NewPaymentStore(db)
		notificationStore = postgres.NewNotificationStore(db)

	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.StorageDriver)
	}

	// 商品缓存只装饰查询用例。创建订单仍读取事实仓库，避免未来商品价格发生
	// 变更时，把缓存中的旧价格写进新订单。
	catalogProducts := products
	loginLimiter := ratelimit.NewFixedWindow(cfg.LoginRateLimit, cfg.AuthRateWindow)
	refreshLimiter := ratelimit.NewFixedWindow(cfg.RefreshRateLimit, cfg.AuthRateWindow)
	if cfg.CacheDriver == config.CacheRedis {
		client, err := redisadapter.Open(ctx, cfg.RedisURL)
		if err != nil {
			closeAll(closers)
			return nil, fmt.Errorf("open redis: %w", err)
		}
		closers = append(closers, client)
		readiness = append(readiness, client)
		catalogProducts = redisadapter.NewCatalogRepository(
			products, client, cfg.RedisKeyPrefix, cfg.CatalogCacheTTL,
			cfg.CatalogNotFoundCacheTTL, cfg.CatalogLoadTimeout, metrics,
		)
		loginLimiter = redisadapter.NewFixedWindowRateLimiter(
			client, cfg.RedisKeyPrefix+":rate:login", cfg.LoginRateLimit, cfg.AuthRateWindow,
		)
		refreshLimiter = redisadapter.NewFixedWindowRateLimiter(
			client, cfg.RedisKeyPrefix+":rate:refresh", cfg.RefreshRateLimit, cfg.AuthRateWindow,
		)
	}

	paymentConsumer := paymentapp.NewConsumer(
		paymentStore, idgenerator.Random{}, clock.System{},
	)
	paymentResultConsumer := orderapp.NewPaymentResultConsumer(orders, clock.System{})
	paymentProvider := paymentprovider.NewResilient(
		paymentprovider.NewSimulatedWithConfig(paymentprovider.SimulatedConfig{
			Delay: cfg.SimulatedPaymentDelay, Failures: cfg.SimulatedPaymentFailures,
		}),
		paymentprovider.ResilienceConfig{
			Timeout:          cfg.PaymentProviderTimeout,
			MaxConcurrent:    cfg.PaymentProviderMaxConcurrent,
			FailureThreshold: cfg.PaymentCircuitFailureThreshold,
			OpenDuration:     cfg.PaymentCircuitOpenDuration,
		},
		metrics,
	)
	notificationProvider := notificationprovider.NewSimulatedWithConfig(notificationprovider.Config{
		Delay: cfg.SimulatedNotificationDelay, Failures: cfg.SimulatedNotificationFailures,
		Rejections:     cfg.SimulatedNotificationRejections,
		ResponseLosses: cfg.SimulatedNotificationResponseLosses,
	})
	notificationConsumer := notificationapp.NewConsumer(
		notificationStore, idgenerator.Random{}, clock.System{},
	)
	return &Services{
		Catalog: catalogapp.NewService(catalogProducts),
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
		Outboxes:             outboxStores,
		Payments:             paymentConsumer,
		PaymentResults:       paymentResultConsumer,
		PaymentQueries:       paymentapp.NewQueryService(paymentStore),
		PaymentTasks:         paymentStore,
		PaymentProvider:      paymentProvider,
		Notifications:        notificationConsumer,
		NotificationQueries:  notificationapp.NewQueryService(notificationStore),
		NotificationWebhooks: notificationapp.NewWebhookService(notificationStore, clock.System{}),
		NotificationTasks:    notificationStore,
		NotificationProvider: notificationProvider,
		LoginLimiter:         loginLimiter,
		RefreshLimiter:       refreshLimiter,
		readiness:            readiness,
		closers:              closers,
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
	if s != nil && s.draining.Load() {
		return ErrDraining
	}
	for _, dependency := range s.readiness {
		if err := dependency.PingContext(ctx); err != nil {
			return fmt.Errorf("check dependency readiness: %w", err)
		}
	}
	return nil
}

// BeginDrain 让 readiness 立即失败，使负载均衡器在进程真正关闭前先摘除实例。
// liveness 不受影响，因为进程仍在正常完成已经接收的工作。
func (s *Services) BeginDrain() {
	if s != nil {
		s.draining.Store(true)
	}
}

func (s *Services) IsDraining() bool {
	return s != nil && s.draining.Load()
}

func (s *Services) Close() error {
	return closeAll(s.closers)
}

func closeAll(closers []io.Closer) error {
	var closeErrors []error
	for index := len(closers) - 1; index >= 0; index-- {
		if err := closers[index].Close(); err != nil {
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
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
