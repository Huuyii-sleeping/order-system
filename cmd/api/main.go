package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/order-system/internal/config"
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

	// ServerMux 是Go标准库提供的HTTP路由器
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(
			"Content-Type", "application/json",
		)

		w.WriteHeader(http.StatusOK)

		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
		})
	})

	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: mux,

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
