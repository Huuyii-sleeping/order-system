package messaging

import (
	"context"
	"fmt"
	"log/slog"

	"example.com/order-system/internal/application/outbox"
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
	if err := handler.Handle(ctx, message); err != nil {
		return fmt.Errorf("handle topic %q: %w", message.Topic, err)
	}
	if p.logger != nil {
		p.logger.InfoContext(ctx, "domain event handled",
			"message_id", message.ID, "topic", message.Topic, "key", message.Key,
		)
	}
	return nil
}
