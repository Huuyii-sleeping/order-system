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

	"example.com/order-system/internal/app"
	outboxapp "example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/config"
	platformmetrics "example.com/order-system/internal/platform/metrics"
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
	metricRegistry := platformmetrics.NewRegistry()

	// main 是组合根：根据配置创建内存或 PostgreSQL 适配器，
	// 再把应用服务注入 HTTP 层。
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	services, err := app.NewServices(startupContext, cfg)
	cancelStartup()
	if err != nil {
		logger.Error("initialize services failed", "error", err)
		os.Exit(1)
	}
	defer services.Close()
	messagingStartupContext, cancelMessagingStartup := context.WithTimeout(context.Background(), 10*time.Second)
	messageRuntime, err := app.NewMessageRuntime(
		messagingStartupContext, cfg, services, logger, metricRegistry,
	)
	cancelMessagingStartup()
	if err != nil {
		logger.Error("initialize messaging failed", "error", err)
		os.Exit(1)
	}
	defer messageRuntime.Close()

	outboxWorkers := make([]*outboxapp.Worker, 0, len(services.Outboxes))
	for _, store := range services.Outboxes {
		outboxWorkers = append(outboxWorkers, outboxapp.NewWorker(
			store, messageRuntime.Publisher, logger, outboxapp.WorkerConfig{
				PollInterval: cfg.OutboxPollInterval, BatchSize: cfg.OutboxBatchSize,
				LeaseDuration: cfg.OutboxLeaseDuration, RetryBase: cfg.OutboxRetryBase,
				RetryMax: cfg.OutboxRetryMax,
			},
		))
	}
	paymentWorker := paymentapp.NewWorker(
		services.PaymentTasks,
		services.PaymentProvider,
		logger,
		paymentapp.WorkerConfig{
			PollInterval: cfg.PaymentPollInterval, BatchSize: cfg.PaymentBatchSize,
			LeaseDuration: cfg.PaymentLeaseDuration, RetryBase: cfg.PaymentRetryBase,
			RetryMax: cfg.PaymentRetryMax,
		},
	)
	workerContext, cancelWorker := context.WithCancel(context.Background())
	workerCount := len(outboxWorkers) + 2
	workerDone := make(chan struct{}, workerCount)
	for _, worker := range outboxWorkers {
		go func() {
			defer func() { workerDone <- struct{}{} }()
			worker.Run(workerContext)
		}()
	}
	go func() {
		defer func() { workerDone <- struct{}{} }()
		paymentWorker.Run(workerContext)
	}()
	go func() {
		defer func() { workerDone <- struct{}{} }()
		if err := messageRuntime.RunConsumer(workerContext); err != nil {
			logger.ErrorContext(workerContext, "message consumer stopped", "error", err)
		}
	}()
	defer func() {
		cancelWorker()
		for range workerCount {
			<-workerDone
		}
	}()

	router := httpapi.NewRouterWithConfig(
		services.Catalog,
		services.Auth,
		services.Orders,
		httpapi.RouterConfig{
			Readiness:        services,
			ReadinessTimeout: cfg.ReadinessTimeout,
			MetricsHandler:   metricRegistry.Handler(),
			PaymentQueries:   services.PaymentQueries,
			Logger:           logger,
			AuthSecurity: httpapi.AuthSecurityConfig{
				SecureCookies: cfg.SecureCookies,
				LoginLimiter: httpapi.NewFixedWindowRateLimiter(
					cfg.LoginRateLimit, cfg.AuthRateWindow,
				),
				RefreshLimiter: httpapi.NewFixedWindowRateLimiter(
					cfg.RefreshRateLimit, cfg.AuthRateWindow,
				),
			},
		},
	)

	// 中间件从内向外组合：
	//
	// router
	//   ← recovery
	//       ← metrics
	//           ← access log
	//               ← request ID
	//
	// Request ID 放在最外层，让后续日志都能关联请求。
	// Access Log 放在 Recovery 外层，让 panic 被转换成 500 后，
	// 仍能记录正确的状态码和响应大小。
	handler := httpapi.WithRequestID(
		httpapi.WithAccessLog(
			logger,
			httpapi.WithHTTPMetrics(
				metricRegistry,
				httpapi.WithRecovery(logger, router),
			),
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
