package telemetry_test

import (
	"context"
	"testing"

	platformtelemetry "example.com/order-system/internal/platform/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestCaptureAndRestoreW3CTraceContext(t *testing.T) {
	originalProvider := otel.GetTracerProvider()
	originalPropagator := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		otel.SetTextMapPropagator(originalPropagator)
	})
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample())))
	otel.SetTextMapPropagator(propagation.TraceContext{})

	parentContext, parent := platformtelemetry.Tracer("test").Start(context.Background(), "parent")
	captured := platformtelemetry.Capture(parentContext)
	parent.End()
	if captured.TraceParent == "" {
		t.Fatal("Capture() traceparent is empty")
	}

	restored := platformtelemetry.Restore(context.Background(), captured)
	restoredSpan := trace.SpanContextFromContext(restored)
	if !restoredSpan.IsValid() || !restoredSpan.IsRemote() {
		t.Fatalf("restored span context = %+v; want valid remote parent", restoredSpan)
	}
	if restoredSpan.TraceID() != trace.SpanContextFromContext(parentContext).TraceID() {
		t.Fatal("restored TraceID does not match captured parent")
	}
}
