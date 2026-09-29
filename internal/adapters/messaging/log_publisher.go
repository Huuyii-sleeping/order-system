// Package messaging 提供异步消息发布适配器。
package messaging

import (
	"context"
	"log/slog"

	"example.com/order-system/internal/application/outbox"
)

// LogPublisher 是只记录事件元数据的诊断适配器。运行中的业务链路使用
// DirectPublisher；替换为 Kafka 时，Worker、Outbox 表和订单事务不需要改变。
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
