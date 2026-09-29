package messaging

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/application/outbox"
	"github.com/twmb/franz-go/pkg/kgo"
)

type syncProducerStub struct {
	record *kgo.Record
	err    error
}

func (s *syncProducerStub) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	s.record = records[0]
	return kgo.ProduceResults{{Record: records[0], Err: s.err}}
}

func TestKafkaPublisherPreservesMessageEnvelope(t *testing.T) {
	producer := &syncProducerStub{}
	publisher := &KafkaPublisher{producer: producer}
	want, err := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"order_id":"order-1"}`),
		time.Date(2026, 9, 29, 8, 0, 0, 123, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := publisher.Publish(context.Background(), want); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	got, err := messageFromRecord(producer.record)
	if err != nil {
		t.Fatalf("messageFromRecord() error = %v", err)
	}
	if got.ID != want.ID || got.Topic != want.Topic || got.Key != want.Key ||
		string(got.Payload) != string(want.Payload) || !got.OccurredAt.Equal(want.OccurredAt) {
		t.Fatalf("round-trip message = %+v, want %+v", got, want)
	}
}

func TestKafkaPublisherReturnsBrokerFailure(t *testing.T) {
	wantErr := errors.New("broker unavailable")
	publisher := &KafkaPublisher{producer: &syncProducerStub{err: wantErr}}
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	if err := publisher.Publish(context.Background(), message); !errors.Is(err, wantErr) {
		t.Fatalf("Publish() error = %v, want %v", err, wantErr)
	}
}

func TestMessageFromRecordRejectsMissingEnvelopeHeaders(t *testing.T) {
	_, err := messageFromRecord(&kgo.Record{
		Topic: "orders.created.v1", Key: []byte("order-1"), Value: []byte(`{"ok":true}`),
	})
	if err == nil {
		t.Fatal("messageFromRecord() error = nil, want missing header error")
	}
}

func TestNewKafkaConsumerRequiresHandlerForEveryTopic(t *testing.T) {
	_, err := NewKafkaConsumer(KafkaConsumerConfig{
		Brokers: []string{"127.0.0.1:9092"}, GroupID: "payments",
		Topics: []string{"orders.created.v1"},
	}, nil, nil)
	if err == nil {
		t.Fatal("NewKafkaConsumer() error = nil, want missing handler error")
	}
}

type consumerClientStub struct {
	fetches     []kgo.Fetches
	fetchIndex  int
	commitCount int
	commitErrs  []error
	resetCount  int
	allowCount  int
	cancel      context.CancelFunc
}

func (s *consumerClientStub) PollRecords(ctx context.Context, _ int) kgo.Fetches {
	if s.fetchIndex < len(s.fetches) {
		fetches := s.fetches[s.fetchIndex]
		s.fetchIndex++
		return fetches
	}
	<-ctx.Done()
	return nil
}

func (s *consumerClientStub) CommitRecords(_ context.Context, _ ...*kgo.Record) error {
	s.commitCount++
	var err error
	if len(s.commitErrs) > 0 {
		err = s.commitErrs[0]
		s.commitErrs = s.commitErrs[1:]
	}
	if err == nil && s.cancel != nil {
		s.cancel()
	}
	return err
}

func (s *consumerClientStub) SetOffsets(map[string]map[int32]kgo.EpochOffset) {
	s.resetCount++
}

func (s *consumerClientStub) AllowRebalance() { s.allowCount++ }

type retryingHandler struct {
	calls int
}

func (h *retryingHandler) Handle(_ context.Context, _ outbox.Message) error {
	h.calls++
	if h.calls == 1 {
		return errors.New("database temporarily unavailable")
	}
	return nil
}

func TestKafkaConsumerCommitsOnlyAfterHandlerSucceeds(t *testing.T) {
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	record, _ := recordFromMessage(message)
	fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{{
		Topic: record.Topic, Partitions: []kgo.FetchPartition{{Records: []*kgo.Record{record}}},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClientStub{fetches: []kgo.Fetches{fetches, fetches}, cancel: cancel}
	handler := &retryingHandler{}
	consumer := &KafkaConsumer{
		client: client, retryInterval: time.Millisecond,
		handlers: map[string]MessageHandler{message.Topic: handler},
	}

	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if handler.calls != 2 || client.commitCount != 1 {
		t.Fatalf("handler calls = %d, commits = %d; want 2 and 1", handler.calls, client.commitCount)
	}
	if client.resetCount != 1 || client.allowCount != 3 {
		t.Fatalf("offset resets = %d, rebalance allows = %d; want 1 and 3", client.resetCount, client.allowCount)
	}
}

func TestKafkaConsumerReprocessesWhenOffsetCommitFails(t *testing.T) {
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	record, _ := recordFromMessage(message)
	fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{{
		Topic: record.Topic, Partitions: []kgo.FetchPartition{{Records: []*kgo.Record{record}}},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClientStub{
		fetches: []kgo.Fetches{fetches, fetches}, cancel: cancel,
		commitErrs: []error{errors.New("coordinator unavailable")},
	}
	handler := &retryingHandler{calls: 1}
	consumer := &KafkaConsumer{
		client: client, retryInterval: time.Millisecond,
		handlers: map[string]MessageHandler{message.Topic: handler},
	}

	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// 第一次业务处理已经成功，但 offset 提交失败，因此同一条消息会再次进入
	// Handler。真实业务 Handler 依靠 Inbox 把第二次调用变成无副作用的重复处理。
	if handler.calls != 3 || client.commitCount != 2 || client.resetCount != 1 {
		t.Fatalf("handler calls = %d, commits = %d, resets = %d; want 3, 2, 1",
			handler.calls, client.commitCount, client.resetCount)
	}
}
