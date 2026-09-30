package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"example.com/order-system/internal/adapters/messaging"
	"example.com/order-system/internal/application/event"
	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/config"
)

type messageConsumer interface {
	Run(context.Context) error
}

// MessageRuntime 是消息基础设施的组合结果。应用层只依赖 Publisher 和 Handler，
// direct/kafka 的选择以及客户端生命周期都留在组合根。
type MessageRuntime struct {
	Publisher outbox.Publisher
	consumer  messageConsumer
	closers   []io.Closer
}

func NewMessageRuntime(
	ctx context.Context, cfg config.Config, services *Services, logger *slog.Logger,
	metrics messaging.KafkaConsumerMetrics,
) (*MessageRuntime, error) {
	handlers := map[string]messaging.MessageHandler{
		event.OrderCreatedV1Topic:     services.Payments,
		event.PaymentSucceededV1Topic: services.PaymentResults,
		event.PaymentFailedV1Topic:    services.PaymentResults,
	}
	switch cfg.MessageDriver {
	case config.MessageDirect:
		return &MessageRuntime{
			Publisher: messaging.NewDirectPublisher(logger, handlers),
		}, nil

	case config.MessageKafka:
		brokers := config.KafkaBrokerList(cfg.KafkaBrokers)
		publisher, err := messaging.NewKafkaPublisher(brokers)
		if err != nil {
			return nil, err
		}
		if err := publisher.PingContext(ctx); err != nil {
			_ = publisher.Close()
			return nil, fmt.Errorf("connect kafka publisher: %w", err)
		}
		consumer, err := messaging.NewKafkaConsumer(messaging.KafkaConsumerConfig{
			Brokers: brokers, GroupID: cfg.KafkaConsumerGroup,
			Topics: []string{
				event.OrderCreatedV1Topic,
				event.PaymentSucceededV1Topic,
				event.PaymentFailedV1Topic,
			},
			DeadLetterTopic: cfg.KafkaDeadLetterTopic,
			MaxAttempts:     cfg.KafkaConsumerMaxAttempts,
			RetryInterval:   cfg.KafkaConsumerRetry,
			Metrics:         metrics,
		}, handlers, logger)
		if err != nil {
			_ = publisher.Close()
			return nil, err
		}
		services.AddReadinessDependency(publisher)
		return &MessageRuntime{
			Publisher: publisher, consumer: consumer,
			closers: []io.Closer{consumer, publisher},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported message driver %q", cfg.MessageDriver)
	}
}

// RunConsumer 在 direct 模式中只等待退出；Kafka 模式中运行消费者组循环。
func (r *MessageRuntime) RunConsumer(ctx context.Context) error {
	if r == nil || r.consumer == nil {
		<-ctx.Done()
		return nil
	}
	return r.consumer.Run(ctx)
}

func (r *MessageRuntime) Close() error {
	if r == nil {
		return nil
	}
	var firstError error
	for _, closer := range r.closers {
		if err := closer.Close(); err != nil && firstError == nil {
			firstError = err
		}
	}
	return firstError
}
