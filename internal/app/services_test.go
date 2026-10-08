package app

import (
	"context"
	"errors"
	"testing"
	"time"

	notificationapp "example.com/order-system/internal/application/notification"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/config"
	domainnotification "example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/domain/order"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type dependencyPingerStub struct {
	err error
}

func (s dependencyPingerStub) PingContext(context.Context) error {
	return s.err
}

func TestServicesReadiness(t *testing.T) {
	t.Run("memory storage has no external dependency", func(t *testing.T) {
		if err := (&Services{}).CheckReadiness(context.Background()); err != nil {
			t.Fatalf("CheckReadiness() error = %v", err)
		}
	})

	t.Run("reports storage failure", func(t *testing.T) {
		wantErr := errors.New("database unavailable")
		services := &Services{readiness: []dependencyPinger{dependencyPingerStub{err: wantErr}}}

		err := services.CheckReadiness(context.Background())

		if !errors.Is(err, wantErr) {
			t.Fatalf("CheckReadiness() error = %v, want wrapped %v", err, wantErr)
		}
	})

	t.Run("checks dependencies added after storage", func(t *testing.T) {
		wantErr := errors.New("broker unavailable")
		services := &Services{}
		services.AddReadinessDependency(pingContextAdapter{err: wantErr})
		if err := services.CheckReadiness(context.Background()); !errors.Is(err, wantErr) {
			t.Fatalf("CheckReadiness() error = %v, want wrapped %v", err, wantErr)
		}
	})

	t.Run("reports draining before checking dependencies", func(t *testing.T) {
		services := &Services{readiness: []dependencyPinger{dependencyPingerStub{}}}
		services.BeginDrain()

		if err := services.CheckReadiness(context.Background()); !errors.Is(err, ErrDraining) {
			t.Fatalf("CheckReadiness() error = %v, want %v", err, ErrDraining)
		}
	})
}

type pingContextAdapter struct{ err error }

func (s pingContextAdapter) PingContext(context.Context) error { return s.err }

func TestNewMemoryServicesWiresAsyncPaymentFlow(t *testing.T) {
	services, err := NewServices(context.Background(), config.Config{
		StorageDriver:  config.StorageMemory,
		AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, BcryptCost: 4,
	}, nil)
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	t.Cleanup(func() { _ = services.Close() })
	if services.Payments == nil || services.PaymentResults == nil || services.PaymentQueries == nil ||
		services.PaymentTasks == nil || services.PaymentProvider == nil || services.Notifications == nil ||
		services.NotificationQueries == nil || services.NotificationTasks == nil ||
		services.NotificationProvider == nil || len(services.Outboxes) != 2 {
		t.Fatal("payment and notification flows, query services, task stores, providers, and memory outboxes must be wired")
	}
}

func TestMemoryServicesCompletesPaymentSaga(t *testing.T) {
	originalProvider := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		otel.SetTextMapPropagator(originalPropagator)
	})
	ctx, rootSpan := provider.Tracer("test").Start(context.Background(), "POST /orders")
	cfg := config.Config{
		StorageDriver: config.StorageMemory, MessageDriver: config.MessageDirect,
		AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, BcryptCost: 4,
	}
	services, err := NewServices(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	t.Cleanup(func() { _ = services.Close() })
	runtime, err := NewMessageRuntime(ctx, cfg, services, nil, nil)
	if err != nil {
		t.Fatalf("NewMessageRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	created, err := services.Orders.CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "customer-1", ProductID: "product-1", Quantity: 2,
		IdempotencyKey: "memory-saga-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	outboxConfig := outbox.WorkerConfig{BatchSize: 10, LeaseDuration: time.Second}
	orderOutbox := outbox.NewWorker(services.Outboxes[0], runtime.Publisher, nil, outboxConfig)
	paymentOutbox := outbox.NewWorker(services.Outboxes[1], runtime.Publisher, nil, outboxConfig)
	if processed, err := orderOutbox.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("order outbox RunOnce() = %d, %v", processed, err)
	}
	paymentWorker := paymentapp.NewWorker(
		services.PaymentTasks, services.PaymentProvider, nil,
		paymentapp.WorkerConfig{BatchSize: 10, LeaseDuration: time.Second},
	)
	if processed, err := paymentWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("payment RunOnce() = %d, %v", processed, err)
	}
	if processed, err := paymentOutbox.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("payment outbox RunOnce() = %d, %v", processed, err)
	}
	// 支付结果消费者把通知请求写入订单模块自己的 Outbox；再次运行同一个
	// Outbox Worker，模拟通知事件被可靠发布并由通知模块消费。
	if processed, err := orderOutbox.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("notification outbox RunOnce() = %d, %v", processed, err)
	}
	notificationWorker := notificationapp.NewWorker(
		services.NotificationTasks, services.NotificationProvider, nil,
		notificationapp.WorkerConfig{BatchSize: 10, LeaseDuration: time.Second}, nil,
	)
	if processed, err := notificationWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("notification RunOnce() = %d, %v", processed, err)
	}

	completed, err := services.Orders.GetOrder(ctx, "customer-1", created.Order.ID())
	if err != nil || completed.Status() != order.StatusConfirmed {
		t.Fatalf("completed order = %+v, %v; want confirmed", completed, err)
	}
	notification, err := services.NotificationQueries.GetNotification(ctx, "customer-1", created.Order.ID())
	if err != nil || notification.Status() != domainnotification.StatusSent {
		t.Fatalf("notification = %+v, %v; want sent", notification, err)
	}
	rootTraceID := rootSpan.SpanContext().TraceID()
	rootSpan.End()
	spans := recorder.Ended()
	if len(spans) != 9 {
		t.Fatalf("ended spans = %d, want root plus eight asynchronous stages", len(spans))
	}
	for _, span := range spans {
		if span.SpanContext().TraceID() != rootTraceID {
			t.Fatalf("span %q TraceID = %s, want %s",
				span.Name(), span.SpanContext().TraceID(), rootTraceID)
		}
	}
}
