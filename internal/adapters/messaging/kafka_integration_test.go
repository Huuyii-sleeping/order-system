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
	consumer, err := NewKafkaConsumer(KafkaConsumerConfig{
		Brokers: brokers, GroupID: group, Topics: []string{topic}, RetryInterval: 10 * time.Millisecond,
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
}
