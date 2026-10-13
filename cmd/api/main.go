package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/order-system/internal/adapters/clock"
	"example.com/order-system/internal/app"
	authapp "example.com/order-system/internal/application/auth"
	notificationapp "example.com/order-system/internal/application/notification"
	outboxapp "example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/config"
	platformlogging "example.com/order-system/internal/platform/logging"
	platformmetrics "example.com/order-system/internal/platform/metrics"
	"example.com/order-system/internal/platform/telemetry"
	"example.com/order-system/internal/transport/httpapi"
)

func main() {
	bootstrapLogger := platformlogging.NewDefault(os.Stdout, "order-system")
	cfg, err := config.Load()
	if err != nil {
		bootstrapLogger.Error("application configuration rejected", "error", err)
		os.Exit(1)
	}
	logger, err := platformlogging.New(os.Stdout, platformlogging.Config{
		ServiceName: cfg.AppName,
		Level:       cfg.LogLevel,
	})
	if err != nil {
		bootstrapLogger.Error("initialize logging", "error", err)
		os.Exit(1)
	}
	if err := run(logger, cfg); err != nil {
		logger.Error("application stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, cfg config.Config) error {
	telemetryStartupContext, cancelTelemetryStartup := context.WithTimeout(context.Background(), 10*time.Second)
	tracerProvider, err := telemetry.NewProvider(telemetryStartupContext, telemetry.Config{
		ServiceName: cfg.AppName, Endpoint: cfg.OTLPTraceEndpoint,
	})
	cancelTelemetryStartup()
	if err != nil {
		return fmt.Errorf("initialize telemetry: %w", err)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracerProvider.Shutdown(shutdownContext); err != nil {
			logger.Error("shutdown telemetry failed", "error", err)
		}
	}()
	metricRegistry := platformmetrics.NewRegistry()

	// main 是组合根：根据配置创建内存或 PostgreSQL 适配器，
	// 再把应用服务注入 HTTP 层。
	startupContext, cancelStartup := context.WithTimeout(context.Background(), 10*time.Second)
	services, err := app.NewServices(startupContext, cfg, metricRegistry)
	cancelStartup()
	if err != nil {
		return fmt.Errorf("initialize services: %w", err)
	}
	defer services.Close()
	messagingStartupContext, cancelMessagingStartup := context.WithTimeout(context.Background(), 10*time.Second)
	messageRuntime, err := app.NewMessageRuntime(
		messagingStartupContext, cfg, services, logger, metricRegistry,
	)
	cancelMessagingStartup()
	if err != nil {
		return fmt.Errorf("initialize messaging: %w", err)
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
			MaxConcurrent: cfg.PaymentWorkerConcurrency,
			LeaseDuration: cfg.PaymentLeaseDuration, RetryBase: cfg.PaymentRetryBase,
			RetryMax: cfg.PaymentRetryMax,
		},
	)
	notificationWorker := notificationapp.NewWorker(
		services.NotificationTasks, services.NotificationProvider, logger,
		notificationapp.WorkerConfig{
			PollInterval: cfg.NotificationPollInterval, BatchSize: cfg.NotificationBatchSize,
			MaxConcurrent: cfg.NotificationWorkerConcurrency,
			LeaseDuration: cfg.NotificationLeaseDuration, RetryBase: cfg.NotificationRetryBase,
			RetryMax: cfg.NotificationRetryMax, ProviderTimeout: cfg.NotificationProviderTimeout,
		}, metricRegistry,
	)
	credentialCleanupWorker := authapp.NewCleanupWorker(
		services.Auth,
		clock.System{},
		logger,
		authapp.CleanupWorkerConfig{
			Interval:  cfg.CredentialCleanupInterval,
			BatchSize: cfg.CredentialCleanupBatchSize,
		},
		metricRegistry,
	)
	backgroundTasks := make([]app.BackgroundTask, 0, len(outboxWorkers)+4)
	for index, worker := range outboxWorkers {
		backgroundTasks = append(backgroundTasks, app.BackgroundTask{
			Name: fmt.Sprintf("outbox-%d", index+1),
			Run: func(stopCtx, workCtx context.Context) error {
				worker.RunGracefully(stopCtx, workCtx)
				return nil
			},
		})
	}
	backgroundTasks = append(backgroundTasks,
		app.BackgroundTask{
			Name: "payment-worker",
			Run: func(stopCtx, workCtx context.Context) error {
				paymentWorker.RunGracefully(stopCtx, workCtx)
				return nil
			},
		},
		app.BackgroundTask{
			Name: "notification-worker",
			Run: func(stopCtx, workCtx context.Context) error {
				notificationWorker.RunGracefully(stopCtx, workCtx)
				return nil
			},
		},
		app.BackgroundTask{
			Name: "auth-credential-cleanup",
			Run: func(stopCtx, workCtx context.Context) error {
				credentialCleanupWorker.RunGracefully(stopCtx, workCtx)
				return nil
			},
		},
		app.BackgroundTask{
			Name: "message-consumer",
			Run:  messageRuntime.RunConsumerGracefully,
		},
	)
	backgroundRuntime := app.StartBackgroundRuntime(logger, backgroundTasks...)
	defer func() {
		backgroundRuntime.ForceStop()
		cleanupContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = backgroundRuntime.Wait(cleanupContext)
	}()

	router := httpapi.NewRouterWithConfig(
		services.Catalog,
		services.Auth,
		services.Orders,
		httpapi.RouterConfig{
			Readiness:            services,
			ReadinessTimeout:     cfg.ReadinessTimeout,
			MetricsHandler:       metricRegistry.Handler(),
			PaymentQueries:       services.PaymentQueries,
			NotificationQueries:  services.NotificationQueries,
			NotificationWebhooks: services.NotificationWebhooks,
			NotificationWebhookSecurity: httpapi.NotificationWebhookSecurity{
				Secret: cfg.NotificationWebhookSecret, Tolerance: cfg.NotificationWebhookTolerance,
			},
			NotificationWebhookMetrics: metricRegistry,
			Logger:                     logger,
			AuthSecurity: httpapi.AuthSecurityConfig{
				SecureCookies:  cfg.SecureCookies,
				LoginLimiter:   services.LoginLimiter,
				RefreshLimiter: services.RefreshLimiter,
			},
		},
	)

	// 中间件从内向外组合：
	//
	// router
	//   ← recovery
	//       ← metrics
	//           ← access log
	//               ← tracing
	//                   ← request ID
	//
	// Request ID 放在最外层，让后续日志都能关联请求。
	// Access Log 放在 Recovery 外层，让 panic 被转换成 500 后，
	// 仍能记录正确的状态码和响应大小。
	handler := httpapi.WithRequestID(
		httpapi.WithTracing(
			httpapi.WithAccessLog(
				logger,
				httpapi.WithHTTPMetrics(
					metricRegistry,
					httpapi.WithRecovery(logger, router),
				),
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
			"address", server.Addr,
		)

		serverErrors <- server.ListenAndServe()
	}()

	var runError error
	serverAlreadyStopped := false
	select {
	case err := <-serverErrors:
		serverAlreadyStopped = true
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runError = fmt.Errorf("http server stopped unexpectedly: %w", err)
		}
	case err := <-backgroundRuntime.Errors():
		runError = err
	case <-shutdownSignal.Done():
		logger.Info("shutdown signal received", "signal_error", shutdownSignal.Err())
	}
	// 恢复系统默认信号行为：第一次信号进入优雅关闭；如果运维人员再次
	// 发送 SIGTERM/SIGINT，则允许立即终止一个无法及时退出的进程。
	stop()

	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(), cfg.ShutdownTimeout,
	)
	defer cancelShutdown()

	// 第一步只让 readiness 失败。此时 liveness、HTTP Server 和后台组件
	// 仍正常工作一小段时间，让负载均衡器完成摘流。
	services.BeginDrain()
	logger.Info("service draining",
		"drain_delay", cfg.ShutdownDrainDelay,
		"shutdown_timeout", cfg.ShutdownTimeout,
	)

	if !serverAlreadyStopped && !waitForDrain(shutdownContext, cfg.ShutdownDrainDelay) {
		runError = errors.Join(runError, errors.New("shutdown deadline exceeded during drain delay"))
	}

	if !serverAlreadyStopped {
		logger.Info("http server shutting down")
		if err := server.Shutdown(shutdownContext); err != nil {
			runError = errors.Join(runError, fmt.Errorf("shutdown http server: %w", err))
			if closeErr := server.Close(); closeErr != nil {
				runError = errors.Join(runError, fmt.Errorf("force close http server: %w", closeErr))
			}
		}

		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			runError = errors.Join(runError, fmt.Errorf("http server stopped: %w", err))
		}
	}

	// HTTP 已经不再接收新请求，才停止后台组件领取下一批任务。这样排水期
	// 内完成的 HTTP 请求产生的 Outbox 记录仍有机会被当前实例处理。
	backgroundRuntime.BeginDrain()
	if err := backgroundRuntime.Wait(shutdownContext); err != nil {
		runError = errors.Join(runError, fmt.Errorf("drain background tasks: %w", err))
		backgroundRuntime.ForceStop()
		forcedContext, cancelForced := context.WithTimeout(context.Background(), time.Second)
		if forcedErr := backgroundRuntime.Wait(forcedContext); forcedErr != nil {
			runError = errors.Join(runError, fmt.Errorf("force stop background tasks: %w", forcedErr))
		}
		cancelForced()
	}

	logger.Info("service stopped")
	return runError
}

func waitForDrain(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
