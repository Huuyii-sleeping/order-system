package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"example.com/order-system/internal/application/messageops"
	"github.com/twmb/franz-go/pkg/kgo"
)

type DeadLetterReplayer struct {
	brokers  []string
	producer syncProducer
	client   *kgo.Client
}

func NewDeadLetterReplayer(brokers []string) (*DeadLetterReplayer, error) {
	normalized, err := normalizeKafkaAddresses(brokers)
	if err != nil {
		return nil, err
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(normalized...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
	)
	if err != nil {
		return nil, fmt.Errorf("create dead-letter replay producer: %w", err)
	}
	return &DeadLetterReplayer{brokers: normalized, producer: client, client: client}, nil
}

func (r *DeadLetterReplayer) ReadDeadLetter(
	ctx context.Context, topic string, partition int32, offset int64,
) (messageops.DeadLetterMessage, error) {
	if r == nil || len(r.brokers) == 0 {
		return messageops.DeadLetterMessage{}, errors.New("dead-letter replayer is not initialized")
	}
	reader, err := kgo.NewClient(
		kgo.SeedBrokers(r.brokers...),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
			topic: {partition: kgo.NewOffset().At(offset)},
		}),
	)
	if err != nil {
		return messageops.DeadLetterMessage{}, fmt.Errorf("create dead-letter reader: %w", err)
	}
	defer reader.Close()

	for {
		fetches := reader.PollRecords(ctx, 1)
		if err := fetches.Err(); err != nil {
			return messageops.DeadLetterMessage{}, fmt.Errorf("fetch dead-letter record: %w", err)
		}
		records := fetches.Records()
		if len(records) == 0 {
			if err := ctx.Err(); err != nil {
				return messageops.DeadLetterMessage{}, err
			}
			continue
		}
		record := records[0]
		if record.Topic != topic || record.Partition != partition || record.Offset != offset {
			return messageops.DeadLetterMessage{}, fmt.Errorf(
				"dead-letter coordinate moved: got %s/%d/%d", record.Topic, record.Partition, record.Offset,
			)
		}
		return decodeDeadLetterForReplay(record)
	}
}

func (r *DeadLetterReplayer) ReplayDeadLetter(
	ctx context.Context, message messageops.DeadLetterMessage,
) error {
	if r == nil || r.producer == nil {
		return errors.New("dead-letter replay producer is not initialized")
	}
	record := &kgo.Record{
		Topic: message.OriginalTopic,
		Key:   append([]byte(nil), message.Key...), Value: append([]byte(nil), message.Value...),
		Headers: make([]kgo.RecordHeader, 0, len(message.Headers)),
	}
	for _, header := range message.Headers {
		record.Headers = append(record.Headers, kgo.RecordHeader{
			Key: header.Key, Value: append([]byte(nil), header.Value...),
		})
	}
	// 重放前再次按正常消费者规则解析业务信封，避免把缺少 message_id 的
	// 手工构造记录送回主 Topic。
	if _, err := messageFromRecord(record); err != nil {
		return fmt.Errorf("validate replay envelope: %w", err)
	}
	if err := r.producer.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("produce replayed dead-letter record: %w", err)
	}
	return nil
}

func (r *DeadLetterReplayer) Close() error {
	if r != nil && r.client != nil {
		r.client.Close()
	}
	return nil
}

func decodeDeadLetterForReplay(record *kgo.Record) (messageops.DeadLetterMessage, error) {
	if record == nil {
		return messageops.DeadLetterMessage{}, errors.New("decode dead-letter replay: nil record")
	}
	originalTopic, err := uniqueHeader(record.Headers, originalTopicHeader)
	if err != nil {
		return messageops.DeadLetterMessage{}, fmt.Errorf("decode dead-letter original topic: %w", err)
	}
	if strings.TrimSpace(originalTopic) == "" {
		return messageops.DeadLetterMessage{}, errors.New("decode dead-letter original topic: empty topic")
	}
	message := messageops.DeadLetterMessage{
		DeadLetterTopic: record.Topic, DeadLetterPartition: record.Partition,
		DeadLetterOffset: record.Offset, OriginalTopic: originalTopic,
		Key: append([]byte(nil), record.Key...), Value: append([]byte(nil), record.Value...),
	}
	for _, header := range record.Headers {
		if strings.HasPrefix(header.Key, deadLetterHeaderPrefix) {
			continue
		}
		message.Headers = append(message.Headers, messageops.Header{
			Key: header.Key, Value: append([]byte(nil), header.Value...),
		})
	}
	probe := &kgo.Record{Topic: message.OriginalTopic, Key: message.Key, Value: message.Value}
	for _, header := range message.Headers {
		probe.Headers = append(probe.Headers, kgo.RecordHeader{Key: header.Key, Value: header.Value})
	}
	if _, err := messageFromRecord(probe); err != nil {
		return messageops.DeadLetterMessage{}, fmt.Errorf("decode dead-letter business envelope: %w", err)
	}
	return message, nil
}
