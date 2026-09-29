// Package messaging 提供异步消息发布适配器。
package messaging

import (
	"context"
	"log/slog"

	"example.com/order-system/internal/application/outbox"
)

// LogPublisher 是当前学习阶段的本地消息出口。替换为 Kafka 等实现时，
// Worker、Outbox 表和订单事务都不需要改变。
type LogPublisher struct {
	logger *slog.Logger
}

func NewLogPublisher(logger *slog.Logger) *LogPublisher {
	return &LogPublisher{logger: logger}
}

func (p *LogPublisher) Publish(ctx context.Context, message outbox.Message) error {
	p.logger.InfoContext(ctx, "domain event published",
		"message_id", message.ID,
		"topic", message.Topic,
		"key", message.Key,
		"attempt", message.Attempts,
	)
	return nil
}
