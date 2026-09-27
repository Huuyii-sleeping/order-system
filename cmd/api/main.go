package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/order-system/internal/adapters/memory"
	catalogapp "example.com/order-system/internal/application/catalog"
	"example.com/order-system/internal/config"
	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/transport/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	// 配置必须在服务启动前加载并校验。
	//
	// 如果配置不合法，服务不应该继续监听端口，
	// 否则可能出现“服务看起来启动了，但行为不正确”的情况。
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	// main 是应用的组合根：具体的内存 Repository 在这里创建，
	// 再注入只依赖接口的应用 Service，最后交给 HTTP 层使用。
	products, err := seedProducts()
	if err != nil {
		logger.Error("seed products failed", "error", err)
		os.Exit(1)
	}

	catalogRepository := memory.NewCatalogRepository(products)
	catalogService := catalogapp.NewService(catalogRepository)
	router := httpapi.NewRouter(catalogService)

	// 中间件从内向外组合：
	//
	// router
	//   ← recovery
	//       ← access log
	//           ← request ID
	//
	// Request ID 放在最外层，让后续日志都能关联请求。
	// Access Log 放在 Recovery 外层，让 panic 被转换成 500 后，
	// 仍能记录正确的状态码和响应大小。
	handler := httpapi.WithRequestID(
		httpapi.WithAccessLog(
			logger,
			httpapi.WithRecovery(logger, router),
		),
	)

	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: handler,

		// 限制发送完整请求头的时间
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       15 * time.Second,
	}

	shutdownSignal, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)

	defer stop()

	serverErrors := make(chan error, 1)

	go func() {
		logger.Info(
			"http server starting",
			"service", cfg.AppName,
			"address", server.Addr,
		)

		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped unexpectedly", "error", err)

			os.Exit(1)
		}

		return

	case <-shutdownSignal.Done():
		logger.Info("shutdown signal received", "signal_error", shutdownSignal.Err())

		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			cfg.ShutdownTimeout,
		)

		defer cancel()

		logger.Info(
			"http server shutting down",
			"timeout", cfg.ShutdownTimeout,
		)

		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("http server shutdown failed", "error", err)

			os.Exit(1)
		}

		err := <-serverErrors
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error(
				"http server stopped unexpectedly",
				"error", err,
			)

			os.Exit(1)
		}

		logger.Info("http server stopped")
	}
}

// seedProducts 提供本地学习阶段使用的初始商品。
// 后续接入 PostgreSQL 后，这部分数据会改为数据库迁移或种子数据。
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
		product, err := catalog.NewProduct(
			definition.id,
			definition.name,
			definition.priceCents,
		)
		if err != nil {
			return nil, err
		}

		products = append(products, product)
	}

	return products, nil
}
