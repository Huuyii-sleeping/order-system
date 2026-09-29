package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"example.com/order-system/internal/application/outbox"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	messageIDHeader  = "order-system-message-id"
	occurredAtHeader = "order-system-occurred-at"
)

type syncProducer interface {
	ProduceSync(context.Context, ...*kgo.Record) kgo.ProduceResults
}

// KafkaPublisher 把 Outbox 消息映射为 Kafka Record。只有 Broker 确认写入后，
// Publish 才返回成功，Outbox Worker 随后才能把数据库记录标记为已发布。
type KafkaPublisher struct {
	producer syncProducer
	client   *kgo.Client
}

func NewKafkaPublisher(brokers []string) (*KafkaPublisher, error) {
	brokers, err := normalizeKafkaAddresses(brokers)
	if err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		// 相同订单 ID 会映射到同一个分区，从而保持同一订单内的事件顺序。
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka producer: %w", err)
	}
	return &KafkaPublisher{producer: client, client: client}, nil
}

func (p *KafkaPublisher) Publish(ctx context.Context, message outbox.Message) error {
	record, err := recordFromMessage(message)
	if err != nil {
		return err
	}
	if err := p.producer.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("produce kafka message %q: %w", message.ID, err)
	}
	return nil
}

func (p *KafkaPublisher) PingContext(ctx context.Context) error {
	if p == nil || p.client == nil {
		return errors.New("kafka producer is not initialized")
	}
	if err := p.client.Ping(ctx); err != nil {
		return fmt.Errorf("ping kafka: %w", err)
	}
	return nil
}

func (p *KafkaPublisher) Close() error {
	if p != nil && p.client != nil {
		p.client.Close()
	}
	return nil
}

type kafkaConsumerClient interface {
	PollRecords(context.Context, int) kgo.Fetches
	CommitRecords(context.Context, ...*kgo.Record) error
	SetOffsets(map[string]map[int32]kgo.EpochOffset)
	AllowRebalance()
}

type KafkaConsumerConfig struct {
	Brokers       []string
	GroupID       string
	Topics        []string
	RetryInterval time.Duration
}

// KafkaConsumer 使用消费者组读取消息，并且只在 Handler 成功后提交 offset。
// Handler 失败时会重试当前记录，因此业务提交和 offset 提交之间仍是至少一次语义；
// 真正防止重复业务效果的是 Handler 内持久化的 Inbox，而不是 Kafka offset。
type KafkaConsumer struct {
	client        kafkaConsumerClient
	kafkaClient   *kgo.Client
	handlers      map[string]MessageHandler
	retryInterval time.Duration
	logger        *slog.Logger
}

func NewKafkaConsumer(
	config KafkaConsumerConfig, handlers map[string]MessageHandler, logger *slog.Logger,
) (*KafkaConsumer, error) {
	brokers, err := normalizeKafkaAddresses(config.Brokers)
	if err != nil {
		return nil, err
	}
	config.GroupID = strings.TrimSpace(config.GroupID)
	if config.GroupID == "" {
		return nil, errors.New("kafka consumer group is required")
	}
	if len(config.Topics) == 0 {
		return nil, errors.New("at least one kafka topic is required")
	}
	for index, topic := range config.Topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return nil, errors.New("kafka topic must not be empty")
		}
		if handler, ok := handlers[topic]; !ok || handler == nil {
			return nil, fmt.Errorf("no handler for kafka topic %q", topic)
		}
		config.Topics[index] = topic
	}
	if config.RetryInterval <= 0 {
		config.RetryInterval = time.Second
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(config.GroupID),
		kgo.ConsumeTopics(config.Topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		// 每次只处理一条记录，并在业务提交和 offset 提交完成后立即放行。
		// 这样提交时分区仍属于当前实例，又不会跨多条任务长期阻塞 rebalance。
		kgo.BlockRebalanceOnPoll(),
	)
	if err != nil {
		return nil, fmt.Errorf("create kafka consumer: %w", err)
	}
	copiedHandlers := make(map[string]MessageHandler, len(handlers))
	for topic, handler := range handlers {
		copiedHandlers[topic] = handler
	}
	return &KafkaConsumer{
		client: client, kafkaClient: client, handlers: copiedHandlers,
		retryInterval: config.RetryInterval, logger: logger,
	}, nil
}

func (c *KafkaConsumer) Run(ctx context.Context) error {
	if c == nil || c.client == nil {
		return errors.New("kafka consumer is not initialized")
	}
	for {
		// 一次只取一条，确保分区内严格按 offset 顺序完成业务和提交。
		fetches := c.client.PollRecords(ctx, 1)
		if ctx.Err() != nil {
			c.client.AllowRebalance()
			return nil
		}
		records := fetches.Records()
		if len(records) == 0 {
			c.client.AllowRebalance()
			if err := fetches.Err(); err != nil {
				c.logError(ctx, "kafka fetch failed", err, nil)
				if !waitForRetry(ctx, c.retryInterval) {
					return nil
				}
			}
			continue
		}

		record := records[0]
		err := c.handleRecord(ctx, record)
		if err == nil {
			err = c.client.CommitRecords(ctx, record)
			if err != nil {
				err = fmt.Errorf("commit kafka offset: %w", err)
			}
		}
		if err != nil {
			// Poll 已推进客户端本地位置。处理或提交失败时必须主动回退，
			// 否则下一条 offset 的提交可能把失败消息一起越过去。
			c.client.SetOffsets(map[string]map[int32]kgo.EpochOffset{
				record.Topic: {
					record.Partition: kgo.NewOffset().At(record.Offset).WithEpoch(record.LeaderEpoch).EpochOffset(),
				},
			})
			c.logError(ctx, "kafka record processing failed", err, record)
		}
		c.client.AllowRebalance()
		if err != nil && !waitForRetry(ctx, c.retryInterval) {
			return nil
		}
	}
}

func (c *KafkaConsumer) handleRecord(ctx context.Context, record *kgo.Record) error {
	message, err := messageFromRecord(record)
	if err != nil {
		return err
	}
	handler, ok := c.handlers[message.Topic]
	if !ok || handler == nil {
		return fmt.Errorf("no handler for kafka topic %q", message.Topic)
	}
	if err := handler.Handle(ctx, message); err != nil {
		return fmt.Errorf("handle kafka topic %q: %w", message.Topic, err)
	}
	return nil
}

func (c *KafkaConsumer) Close() error {
	if c != nil && c.kafkaClient != nil {
		c.kafkaClient.Close()
	}
	return nil
}

func (c *KafkaConsumer) logError(
	ctx context.Context, message string, err error, record *kgo.Record,
) {
	if c.logger == nil {
		return
	}
	attributes := []any{"error", err}
	if record != nil {
		attributes = append(attributes,
			"topic", record.Topic, "partition", record.Partition, "offset", record.Offset,
		)
	}
	c.logger.ErrorContext(ctx, message, attributes...)
}

func recordFromMessage(message outbox.Message) (*kgo.Record, error) {
	normalized, err := outbox.NewMessage(
		message.ID, message.Topic, message.Key, message.Payload, message.OccurredAt,
	)
	if err != nil {
		return nil, fmt.Errorf("encode kafka message: %w", err)
	}
	return &kgo.Record{
		Topic: normalized.Topic, Key: []byte(normalized.Key), Value: append([]byte(nil), normalized.Payload...),
		Timestamp: normalized.OccurredAt,
		Headers: []kgo.RecordHeader{
			{Key: messageIDHeader, Value: []byte(normalized.ID)},
			{Key: occurredAtHeader, Value: []byte(normalized.OccurredAt.Format(time.RFC3339Nano))},
		},
	}, nil
}

func messageFromRecord(record *kgo.Record) (outbox.Message, error) {
	if record == nil {
		return outbox.Message{}, errors.New("decode kafka message: nil record")
	}
	messageID, err := uniqueHeader(record.Headers, messageIDHeader)
	if err != nil {
		return outbox.Message{}, fmt.Errorf("decode kafka message: %w", err)
	}
	occurredAtRaw, err := uniqueHeader(record.Headers, occurredAtHeader)
	if err != nil {
		return outbox.Message{}, fmt.Errorf("decode kafka message: %w", err)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, occurredAtRaw)
	if err != nil {
		return outbox.Message{}, fmt.Errorf("decode kafka occurred_at: %w", err)
	}
	message, err := outbox.NewMessage(
		messageID, record.Topic, string(record.Key), record.Value, occurredAt,
	)
	if err != nil {
		return outbox.Message{}, fmt.Errorf("decode kafka message: %w", err)
	}
	return message, nil
}

func uniqueHeader(headers []kgo.RecordHeader, key string) (string, error) {
	var value string
	found := false
	for _, header := range headers {
		if header.Key != key {
			continue
		}
		if found {
			return "", fmt.Errorf("duplicate kafka header %q", key)
		}
		found = true
		value = string(header.Value)
	}
	if !found || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing kafka header %q", key)
	}
	return value, nil
}

func normalizeKafkaAddresses(brokers []string) ([]string, error) {
	if len(brokers) == 0 {
		return nil, errors.New("at least one kafka broker is required")
	}
	normalized := make([]string, len(brokers))
	for index, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			return nil, errors.New("kafka broker address must not be empty")
		}
		normalized[index] = broker
	}
	return normalized, nil
}

func waitForRetry(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
