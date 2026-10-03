package app

import (
	"context"
	"errors"
	"testing"
	"time"

	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/config"
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
}

type pingContextAdapter struct{ err error }

func (s pingContextAdapter) PingContext(context.Context) error { return s.err }

func TestNewMemoryServicesWiresAsyncPaymentFlow(t *testing.T) {
	services, err := NewServices(context.Background(), config.Config{
		StorageDriver:  config.StorageMemory,
		AccessTokenTTL: time.Minute, RefreshTokenTTL: time.Hour, BcryptCost: 4,
	})
	if err != nil {
		t.Fatalf("NewServices() error = %v", err)
	}
	t.Cleanup(func() { _ = services.Close() })
	if services.Payments == nil || services.PaymentResults == nil || services.PaymentQueries == nil ||
		services.PaymentTasks == nil || services.PaymentProvider == nil || len(services.Outboxes) != 2 {
		t.Fatal("payment flow, both memory outboxes, query service, task store, and provider must be wired")
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
	services, err := NewServices(ctx, cfg)
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

	completed, err := services.Orders.GetOrder(ctx, "customer-1", created.Order.ID())
	if err != nil || completed.Status() != order.StatusConfirmed {
		t.Fatalf("completed order = %+v, %v; want confirmed", completed, err)
	}
	rootTraceID := rootSpan.SpanContext().TraceID()
	rootSpan.End()
	spans := recorder.Ended()
	if len(spans) != 6 {
		t.Fatalf("ended spans = %d, want root plus five asynchronous stages", len(spans))
	}
	for _, span := range spans {
		if span.SpanContext().TraceID() != rootTraceID {
			t.Fatalf("span %q TraceID = %s, want %s",
				span.Name(), span.SpanContext().TraceID(), rootTraceID)
		}
	}
}
