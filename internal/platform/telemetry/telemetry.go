// Package telemetry 负责初始化 OpenTelemetry，并提供跨异步边界传递 Trace Context 的辅助函数。
package telemetry

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// PropagationContext 是需要跟随 Outbox 消息和后台任务持久化的 W3C Trace Context。
// 它只包含链路身份，不保存进程内的 context.Context，也不携带业务数据。
type PropagationContext struct {
	TraceParent string `json:"traceparent,omitempty"`
	TraceState  string `json:"tracestate,omitempty"`
}

type Config struct {
	ServiceName string
	Endpoint    string
}

// NewProvider 创建进程级 TracerProvider。Endpoint 为空时使用不采样 Provider，
// 因此追踪默认不会产生网络请求，也不会影响没有启动 Tempo 的开发流程。
func NewProvider(ctx context.Context, config Config) (*sdktrace.TracerProvider, error) {
	serviceName := strings.TrimSpace(config.ServiceName)
	if serviceName == "" {
		return nil, fmt.Errorf("telemetry service name is required")
	}

	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resource.NewWithAttributes(
			"",
			attribute.String("service.name", serviceName),
		)),
	}
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		options = append(options, sdktrace.WithSampler(sdktrace.NeverSample()))
	} else {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		options = append(options,
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		)
	}

	provider := sdktrace.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return provider, nil
}

func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// Capture 把当前 Span Context 编码成稳定的 W3C 字段，供数据库持久化。
func Capture(ctx context.Context) PropagationContext {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return PropagationContext{
		TraceParent: carrier.Get("traceparent"),
		TraceState:  carrier.Get("tracestate"),
	}
}

// Restore 从持久化字段恢复远端父级。无效或空字段会被传播器安全忽略。
func Restore(ctx context.Context, captured PropagationContext) context.Context {
	carrier := propagation.MapCarrier{}
	if captured.TraceParent != "" {
		carrier.Set("traceparent", captured.TraceParent)
	}
	if captured.TraceState != "" {
		carrier.Set("tracestate", captured.TraceState)
	}
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}
