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

	"example.com/order-system/internal/config"
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

	// main 负责组装 HTTP Handler，不关心具体有哪些路由。
	// 路由和协议响应由 transport/httpapi 包维护。
	router := httpapi.NewRouter()

	// 中间件从内向外组合：
	//
	// router
	//   ← access log
	//       ← request ID
	//
	// Request ID 放在最外层，确保 Access Log 读取到已经写入 Context
	// 的请求 ID。
	handler := httpapi.WithRequestID(
		httpapi.WithAccessLog(logger, router),
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
