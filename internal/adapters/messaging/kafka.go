package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/platform/telemetry"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	messageIDHeader   = "order-system-message-id"
	occurredAtHeader  = "order-system-occurred-at"
	traceParentHeader = "traceparent"
	traceStateHeader  = "tracestate"
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
	if !trace.SpanContextFromContext(ctx).IsValid() {
		ctx = telemetry.Restore(ctx, message.TraceContext)
	}
	ctx, span := telemetry.Tracer("order-system/messaging").Start(
		ctx, message.Topic+" publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", message.Topic),
			attribute.String("messaging.message.id", message.ID),
		),
	)
	defer span.End()
	message.TraceContext = telemetry.Capture(ctx)
	record, err := recordFromMessage(message)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	if err := p.producer.ProduceSync(ctx, record).FirstErr(); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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
	Brokers         []string
	GroupID         string
	Topics          []string
	DeadLetterTopic string
	MaxAttempts     int
	RetryInterval   time.Duration
	Metrics         KafkaConsumerMetrics
}

// KafkaConsumerMetrics 由平台层实现。消息适配器只报告固定维度的事实，
// 不依赖 Prometheus 类型，也不把消息 ID 或错误文本作为高基数标签。
type KafkaConsumerMetrics interface {
	RecordProcessingAttempt(consumerGroup, topic, outcome string)
	RecordRetry(consumerGroup, topic, reason string)
	RecordDeadLetter(consumerGroup, topic string)
	RecordOffsetCommit(consumerGroup, topic, outcome string)
	SetConsumerLag(consumerGroup, topic string, partition int32, lag int64)
	DeleteConsumerLag(consumerGroup, topic string, partition int32)
}

// KafkaConsumer 使用消费者组读取消息，并且只在 Handler 成功后提交 offset。
// Handler 失败时会重试当前记录，因此业务提交和 offset 提交之间仍是至少一次语义；
// 真正防止重复业务效果的是 Handler 内持久化的 Inbox，而不是 Kafka offset。
type KafkaConsumer struct {
	client          kafkaConsumerClient
	kafkaClient     *kgo.Client
	dlqProducer     syncProducer
	handlers        map[string]MessageHandler
	groupID         string
	deadLetterTopic string
	maxAttempts     int
	retryInterval   time.Duration
	metrics         KafkaConsumerMetrics
	logger          *slog.Logger
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
	config.DeadLetterTopic = strings.TrimSpace(config.DeadLetterTopic)
	if config.DeadLetterTopic == "" {
		return nil, errors.New("kafka dead letter topic is required")
	}
	for index, topic := range config.Topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return nil, errors.New("kafka topic must not be empty")
		}
		if handler, ok := handlers[topic]; !ok || handler == nil {
			return nil, fmt.Errorf("no handler for kafka topic %q", topic)
		}
		if topic == config.DeadLetterTopic {
			return nil, fmt.Errorf("kafka dead letter topic %q must not be consumed as a business topic", topic)
		}
		config.Topics[index] = topic
	}
	if config.MaxAttempts <= 0 {
		return nil, errors.New("kafka consumer max attempts must be greater than zero")
	}
	if config.RetryInterval <= 0 {
		config.RetryInterval = time.Second
	}
	clearRevokedLag := func(
		_ context.Context, _ *kgo.Client, partitions map[string][]int32,
	) {
		deleteConsumerLagMetrics(config.Metrics, config.GroupID, partitions)
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ConsumerGroup(config.GroupID),
		kgo.ConsumeTopics(config.Topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.OnPartitionsRevoked(clearRevokedLag),
		kgo.OnPartitionsLost(clearRevokedLag),
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
		dlqProducer: client, groupID: config.GroupID, deadLetterTopic: config.DeadLetterTopic,
		maxAttempts: config.MaxAttempts, retryInterval: config.RetryInterval,
		metrics: config.Metrics, logger: logger,
	}, nil
}

func deleteConsumerLagMetrics(
	metrics KafkaConsumerMetrics, groupID string, partitions map[string][]int32,
) {
	if metrics == nil {
		return
	}
	for topic, assignedPartitions := range partitions {
		for _, partition := range assignedPartitions {
			metrics.DeleteConsumerLag(groupID, topic, partition)
		}
	}
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
		highWatermark := fetchHighWatermark(fetches, record)
		c.setConsumerLag(record, highWatermark, false)
		handlerErr, attempts := c.handleRecordWithRetry(ctx, record)
		if ctx.Err() != nil {
			c.client.AllowRebalance()
			return nil
		}

		err := handlerErr
		if handlerErr != nil {
			// 达到有限重试次数后，把原始记录和失败上下文写入 DLQ。只有
			// Broker 确认 DLQ 写入成功，才允许提交原消息 offset。
			err = c.publishDeadLetter(ctx, record, attempts, handlerErr)
			if err == nil {
				c.recordDeadLetter(record)
				c.logDeadLetter(ctx, record, attempts, handlerErr)
			} else {
				c.recordRetry(record, "dead_letter_publish")
			}
		}
		if err == nil {
			err = c.client.CommitRecords(ctx, record)
			if err != nil {
				c.recordOffsetCommit(record, "failure")
				c.recordRetry(record, "offset_commit")
				err = fmt.Errorf("commit kafka offset: %w", err)
			} else {
				c.recordOffsetCommit(record, "success")
				c.setConsumerLag(record, highWatermark, true)
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

func (c *KafkaConsumer) handleRecordWithRetry(
	ctx context.Context, record *kgo.Record,
) (error, int) {
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		err := c.handleRecord(ctx, record)
		if err == nil {
			c.recordProcessingAttempt(record, "success")
			return nil, attempt
		}
		c.recordProcessingAttempt(record, "failure")
		if attempt == c.maxAttempts {
			return err, attempt
		}
		c.logRetry(ctx, record, attempt, err)
		c.recordRetry(record, "handler")
		if !waitForRetry(ctx, c.retryInterval) {
			return ctx.Err(), attempt
		}
	}
	return errors.New("kafka record retry loop exhausted unexpectedly"), c.maxAttempts
}

func fetchHighWatermark(fetches kgo.Fetches, record *kgo.Record) int64 {
	if record == nil {
		return 0
	}
	var highWatermark int64
	fetches.EachPartition(func(partition kgo.FetchTopicPartition) {
		if partition.Topic == record.Topic && partition.Partition == record.Partition {
			highWatermark = partition.HighWatermark
		}
	})
	return highWatermark
}

func (c *KafkaConsumer) recordProcessingAttempt(record *kgo.Record, outcome string) {
	if c.metrics != nil && record != nil {
		c.metrics.RecordProcessingAttempt(c.groupID, record.Topic, outcome)
	}
}

func (c *KafkaConsumer) recordRetry(record *kgo.Record, reason string) {
	if c.metrics != nil && record != nil {
		c.metrics.RecordRetry(c.groupID, record.Topic, reason)
	}
}

func (c *KafkaConsumer) recordDeadLetter(record *kgo.Record) {
	if c.metrics != nil && record != nil {
		c.metrics.RecordDeadLetter(c.groupID, record.Topic)
	}
}

func (c *KafkaConsumer) recordOffsetCommit(record *kgo.Record, outcome string) {
	if c.metrics != nil && record != nil {
		c.metrics.RecordOffsetCommit(c.groupID, record.Topic, outcome)
	}
}

func (c *KafkaConsumer) setConsumerLag(record *kgo.Record, highWatermark int64, committed bool) {
	if c.metrics == nil || record == nil || highWatermark <= 0 {
		return
	}
	nextOffset := record.Offset
	if committed {
		nextOffset++
	}
	c.metrics.SetConsumerLag(c.groupID, record.Topic, record.Partition, highWatermark-nextOffset)
}

func (c *KafkaConsumer) publishDeadLetter(
	ctx context.Context, record *kgo.Record, attempts int, processingErr error,
) error {
	if c.dlqProducer == nil {
		return errors.New("kafka dead letter producer is not initialized")
	}
	deadLetter, err := deadLetterRecord(
		record, c.deadLetterTopic, c.groupID, attempts, processingErr, time.Now(),
	)
	if err != nil {
		return err
	}
	if err := c.dlqProducer.ProduceSync(ctx, deadLetter).FirstErr(); err != nil {
		return fmt.Errorf("produce kafka dead letter: %w", err)
	}
	return nil
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
	consumerContext := telemetry.Restore(ctx, message.TraceContext)
	consumerContext, span := telemetry.Tracer("order-system/messaging").Start(
		consumerContext, message.Topic+" process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "kafka"),
			attribute.String("messaging.destination.name", message.Topic),
			attribute.String("messaging.message.id", message.ID),
			attribute.Int64("messaging.kafka.offset", record.Offset),
			attribute.Int("messaging.kafka.partition", int(record.Partition)),
		),
	)
	defer span.End()
	if err := handler.Handle(consumerContext, message); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
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

func (c *KafkaConsumer) logRetry(
	ctx context.Context, record *kgo.Record, attempt int, err error,
) {
	if c.logger == nil {
		return
	}
	c.logger.WarnContext(ctx, "kafka record handler will retry",
		"error", err, "topic", record.Topic, "partition", record.Partition,
		"offset", record.Offset, "attempt", attempt, "max_attempts", c.maxAttempts,
	)
}

func (c *KafkaConsumer) logDeadLetter(
	ctx context.Context, record *kgo.Record, attempts int, processingErr error,
) {
	if c.logger == nil {
		return
	}
	c.logger.WarnContext(ctx, "kafka record moved to dead letter topic",
		"error", processingErr, "topic", record.Topic, "partition", record.Partition,
		"offset", record.Offset, "attempts", attempts, "dead_letter_topic", c.deadLetterTopic,
	)
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
		Headers: appendTraceHeaders([]kgo.RecordHeader{
			{Key: messageIDHeader, Value: []byte(normalized.ID)},
			{Key: occurredAtHeader, Value: []byte(normalized.OccurredAt.Format(time.RFC3339Nano))},
		}, message.TraceContext),
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
	traceParent, err := optionalUniqueHeader(record.Headers, traceParentHeader)
	if err != nil {
		return outbox.Message{}, fmt.Errorf("decode kafka message: %w", err)
	}
	traceState, err := optionalUniqueHeader(record.Headers, traceStateHeader)
	if err != nil {
		return outbox.Message{}, fmt.Errorf("decode kafka message: %w", err)
	}
	message.TraceContext = telemetry.PropagationContext{TraceParent: traceParent, TraceState: traceState}
	return message, nil
}

func appendTraceHeaders(
	headers []kgo.RecordHeader, captured telemetry.PropagationContext,
) []kgo.RecordHeader {
	if captured.TraceParent != "" {
		headers = append(headers, kgo.RecordHeader{Key: traceParentHeader, Value: []byte(captured.TraceParent)})
	}
	if captured.TraceState != "" {
		headers = append(headers, kgo.RecordHeader{Key: traceStateHeader, Value: []byte(captured.TraceState)})
	}
	return headers
}

func uniqueHeader(headers []kgo.RecordHeader, key string) (string, error) {
	value, err := optionalUniqueHeader(headers, key)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("missing kafka header %q", key)
	}
	return value, nil
}

func optionalUniqueHeader(headers []kgo.RecordHeader, key string) (string, error) {
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
	if !found {
		return "", nil
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
