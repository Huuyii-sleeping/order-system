package messaging

import (
	"context"
	"fmt"
	"log/slog"

	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/platform/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type MessageHandler interface {
	Handle(context.Context, outbox.Message) error
}

// DirectPublisher 是 Kafka 接入前的进程内消息适配器。它保留真实的发布/消费
// 边界和失败语义，但在当前进程中同步调用 Handler，便于验证完整一致性链路。
type DirectPublisher struct {
	logger   *slog.Logger
	handlers map[string]MessageHandler
}

func NewDirectPublisher(
	logger *slog.Logger, handlers map[string]MessageHandler,
) *DirectPublisher {
	copied := make(map[string]MessageHandler, len(handlers))
	for topic, handler := range handlers {
		copied[topic] = handler
	}
	return &DirectPublisher{logger: logger, handlers: copied}
}

func (p *DirectPublisher) Publish(ctx context.Context, message outbox.Message) error {
	handler, ok := p.handlers[message.Topic]
	if !ok || handler == nil {
		return fmt.Errorf("no handler for topic %q", message.Topic)
	}
	consumerContext := ctx
	if !trace.SpanContextFromContext(ctx).IsValid() {
		consumerContext = telemetry.Restore(ctx, message.TraceContext)
	}
	consumerContext, span := telemetry.Tracer("order-system/messaging").Start(
		consumerContext, message.Topic+" process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "direct"),
			attribute.String("messaging.destination.name", message.Topic),
			attribute.String("messaging.message.id", message.ID),
		),
	)
	defer span.End()
	if err := handler.Handle(consumerContext, message); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("handle topic %q: %w", message.Topic, err)
	}
	if p.logger != nil {
		p.logger.InfoContext(consumerContext, "domain event handled",
			"message_id", message.ID, "topic", message.Topic, "key", message.Key,
			"trace_id", telemetry.TraceID(consumerContext),
		)
	}
	return nil
}
