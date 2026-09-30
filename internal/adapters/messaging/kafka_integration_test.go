package messaging

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/order-system/internal/application/outbox"
	"github.com/twmb/franz-go/pkg/kgo"
)

type recordingMessageHandler struct {
	mu       sync.Mutex
	messages []outbox.Message
	targetID string
}

type commitNotifyingClient struct {
	client    *kgo.Client
	targetID  string
	committed chan struct{}
}

func (c *commitNotifyingClient) PollRecords(ctx context.Context, maxRecords int) kgo.Fetches {
	return c.client.PollRecords(ctx, maxRecords)
}

func (c *commitNotifyingClient) CommitRecords(ctx context.Context, records ...*kgo.Record) error {
	if err := c.client.CommitRecords(ctx, records...); err != nil {
		return err
	}
	for _, record := range records {
		messageID, err := uniqueHeader(record.Headers, messageIDHeader)
		if err == nil && messageID == c.targetID {
			c.committed <- struct{}{}
		}
	}
	return nil
}

func (c *commitNotifyingClient) SetOffsets(offsets map[string]map[int32]kgo.EpochOffset) {
	c.client.SetOffsets(offsets)
}

func (c *commitNotifyingClient) AllowRebalance() { c.client.AllowRebalance() }

func (h *recordingMessageHandler) Handle(_ context.Context, message outbox.Message) error {
	h.mu.Lock()
	h.messages = append(h.messages, message)
	h.mu.Unlock()
	return nil
}

func (h *recordingMessageHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, message := range h.messages {
		if message.ID == h.targetID {
			count++
		}
	}
	return count
}

func TestIntegrationKafkaPublishesConsumesAndCommits(t *testing.T) {
	brokerValue := os.Getenv("TEST_KAFKA_BROKERS")
	if strings.TrimSpace(brokerValue) == "" {
		t.Skip("TEST_KAFKA_BROKERS is not set")
	}
	brokers := strings.Split(brokerValue, ",")
	// 集成测试使用独立 Topic，避免测试信封进入正式业务消费者的分区。
	topic := "orders.created.v1.integration"
	group := "order-system-integration-v1"
	messageID := fmt.Sprintf("integration-message-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	publisher, err := NewKafkaPublisher(brokers)
	if err != nil {
		t.Fatalf("NewKafkaPublisher() error = %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close() })

	handler := &recordingMessageHandler{targetID: messageID}
	metrics := newKafkaMetricsStub()
	consumer, err := NewKafkaConsumer(KafkaConsumerConfig{
		Brokers: brokers, GroupID: group, Topics: []string{topic},
		DeadLetterTopic: "orders.created.v1.integration.dlq", MaxAttempts: 3,
		RetryInterval: 10 * time.Millisecond, Metrics: metrics,
	}, map[string]MessageHandler{topic: handler}, nil)
	if err != nil {
		t.Fatalf("NewKafkaConsumer() error = %v", err)
	}
	t.Cleanup(func() { _ = consumer.Close() })
	committed := make(chan struct{}, 2)
	consumer.client = &commitNotifyingClient{
		client: consumer.kafkaClient, targetID: messageID, committed: committed,
	}
	consumerContext, stopConsumer := context.WithCancel(ctx)
	consumerDone := make(chan error, 1)
	go func() { consumerDone <- consumer.Run(consumerContext) }()

	message, err := outbox.NewMessage(
		messageID, topic, "order-1", []byte(`{"order_id":"order-1"}`), time.Now(),
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	// 相同消息写入两个不同 offset，证明 Broker 自身不会替消费者去重。
	for range 2 {
		if err := publisher.Publish(ctx, message); err != nil {
			t.Fatalf("Publish() error = %v", err)
		}
	}
	for range 2 {
		select {
		case <-committed:
		case <-ctx.Done():
			t.Fatalf("wait for committed kafka messages: %v", ctx.Err())
		}
	}
	if got := handler.count(); got != 2 {
		t.Fatalf("handled messages = %d, want 2", got)
	}

	stopConsumer()
	select {
	case err := <-consumerDone:
		if err != nil {
			t.Fatalf("consumer.Run() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("wait for consumer shutdown: %v", ctx.Err())
	}
	if metrics.processing["success"] < 2 || metrics.commits["success"] < 2 {
		t.Fatalf("metrics = %+v; want at least two processing and commit successes", metrics)
	}
	if len(metrics.lags) == 0 || metrics.lags[len(metrics.lags)-1] != 0 {
		t.Fatalf("lag observations = %v, want final lag 0", metrics.lags)
	}
}

type poisonThenSuccessHandler struct {
	mu           sync.Mutex
	poisonID     string
	successID    string
	poisonCalls  int
	successCalls int
}

func (h *poisonThenSuccessHandler) Handle(_ context.Context, message outbox.Message) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch message.ID {
	case h.poisonID:
		h.poisonCalls++
		return fmt.Errorf("poison integration message")
	case h.successID:
		h.successCalls++
	}
	return nil
}

func (h *poisonThenSuccessHandler) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.poisonCalls, h.successCalls
}

func TestIntegrationKafkaMovesPoisonRecordToDeadLetterAndContinues(t *testing.T) {
	brokerValue := os.Getenv("TEST_KAFKA_BROKERS")
	if strings.TrimSpace(brokerValue) == "" {
		t.Skip("TEST_KAFKA_BROKERS is not set")
	}
	brokers := strings.Split(brokerValue, ",")
	topic := "orders.created.v1.integration.poison"
	dlqTopic := "orders.created.v1.integration.dlq"
	testID := time.Now().UnixNano()
	poisonID := fmt.Sprintf("integration-poison-%d", testID)
	successID := fmt.Sprintf("integration-success-%d", testID)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	publisher, err := NewKafkaPublisher(brokers)
	if err != nil {
		t.Fatalf("NewKafkaPublisher() error = %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close() })

	dlqClient, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(fmt.Sprintf("order-system-dlq-observer-%d", testID)),
		kgo.ConsumeTopics(dlqTopic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatalf("create DLQ observer: %v", err)
	}
	t.Cleanup(dlqClient.Close)

	handler := &poisonThenSuccessHandler{poisonID: poisonID, successID: successID}
	metrics := newKafkaMetricsStub()
	consumer, err := NewKafkaConsumer(KafkaConsumerConfig{
		Brokers: brokers, GroupID: fmt.Sprintf("order-system-poison-%d", testID),
		Topics: []string{topic}, DeadLetterTopic: dlqTopic, MaxAttempts: 2,
		RetryInterval: 10 * time.Millisecond, Metrics: metrics,
	}, map[string]MessageHandler{topic: handler}, nil)
	if err != nil {
		t.Fatalf("NewKafkaConsumer() error = %v", err)
	}
	t.Cleanup(func() { _ = consumer.Close() })
	committed := make(chan struct{}, 1)
	consumer.client = &commitNotifyingClient{
		client: consumer.kafkaClient, targetID: successID, committed: committed,
	}
	consumerContext, stopConsumer := context.WithCancel(ctx)
	t.Cleanup(stopConsumer)
	consumerDone := make(chan error, 1)
	go func() { consumerDone <- consumer.Run(consumerContext) }()

	for _, id := range []string{poisonID, successID} {
		message, messageErr := outbox.NewMessage(
			id, topic, "order-1", []byte(fmt.Sprintf(`{"message_id":%q}`, id)), time.Now(),
		)
		if messageErr != nil {
			t.Fatalf("NewMessage(%q) error = %v", id, messageErr)
		}
		if err := publisher.Publish(ctx, message); err != nil {
			t.Fatalf("Publish(%q) error = %v", id, err)
		}
	}

	var deadLetter *kgo.Record
	for deadLetter == nil && ctx.Err() == nil {
		fetches := dlqClient.PollRecords(ctx, 10)
		if err := fetches.Err(); err != nil {
			t.Fatalf("poll DLQ: %v", err)
		}
		for _, record := range fetches.Records() {
			messageID, headerErr := uniqueHeader(record.Headers, messageIDHeader)
			if headerErr == nil && messageID == poisonID {
				deadLetter = record
				break
			}
		}
	}
	if deadLetter == nil {
		t.Fatalf("wait for dead letter: %v", ctx.Err())
	}
	select {
	case <-committed:
	case <-ctx.Done():
		t.Fatalf("wait for successful message offset commit: %v", ctx.Err())
	}
	poisonCalls, successCalls := handler.counts()
	if poisonCalls != 2 || successCalls != 1 {
		t.Fatalf("handler calls = poison:%d success:%d; want 2 and 1", poisonCalls, successCalls)
	}
	if got, err := uniqueHeader(deadLetter.Headers, originalTopicHeader); err != nil || got != topic {
		t.Fatalf("DLQ original topic = %q, %v; want %q, nil", got, err, topic)
	}
	if got, err := uniqueHeader(deadLetter.Headers, deliveryAttemptsHeader); err != nil || got != "2" {
		t.Fatalf("DLQ attempts = %q, %v; want 2, nil", got, err)
	}

	stopConsumer()
	select {
	case err := <-consumerDone:
		if err != nil {
			t.Fatalf("consumer.Run() error = %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("wait for consumer shutdown: %v", ctx.Err())
	}
	if metrics.processing["failure"] < 2 || metrics.processing["success"] < 1 ||
		metrics.retries["handler"] < 1 || metrics.deadLetters < 1 ||
		metrics.commits["success"] < 2 {
		t.Fatalf("metrics = %+v; want poison failures, retry, DLQ and successful commits", metrics)
	}
}
