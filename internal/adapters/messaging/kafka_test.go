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
	record  *kgo.Record
	err     error
	cancel  context.CancelFunc
	produce int
}

func (s *syncProducerStub) ProduceSync(_ context.Context, records ...*kgo.Record) kgo.ProduceResults {
	s.record = records[0]
	s.produce++
	if s.cancel != nil {
		s.cancel()
	}
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
		Topics: []string{"orders.created.v1"}, DeadLetterTopic: "orders.created.v1.dlq",
		MaxAttempts: 3,
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

type kafkaMetricsStub struct {
	processing  map[string]int
	retries     map[string]int
	deadLetters int
	commits     map[string]int
	lags        []int64
	deletedLags []int32
}

func newKafkaMetricsStub() *kafkaMetricsStub {
	return &kafkaMetricsStub{
		processing: make(map[string]int), retries: make(map[string]int), commits: make(map[string]int),
	}
}

func (m *kafkaMetricsStub) RecordProcessingAttempt(_, _, outcome string) {
	m.processing[outcome]++
}

func (m *kafkaMetricsStub) RecordRetry(_, _, reason string) {
	m.retries[reason]++
}

func (m *kafkaMetricsStub) RecordDeadLetter(_, _ string) { m.deadLetters++ }

func (m *kafkaMetricsStub) RecordOffsetCommit(_, _, outcome string) {
	m.commits[outcome]++
}

func (m *kafkaMetricsStub) SetConsumerLag(_ string, _ string, _ int32, lag int64) {
	m.lags = append(m.lags, lag)
}

func (m *kafkaMetricsStub) DeleteConsumerLag(_ string, _ string, partition int32) {
	m.deletedLags = append(m.deletedLags, partition)
}

func TestKafkaConsumerCommitsOnlyAfterHandlerSucceeds(t *testing.T) {
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	record, _ := recordFromMessage(message)
	fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{{
		Topic: record.Topic, Partitions: []kgo.FetchPartition{{HighWatermark: 8, Records: []*kgo.Record{record}}},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClientStub{fetches: []kgo.Fetches{fetches, fetches}, cancel: cancel}
	handler := &retryingHandler{}
	metrics := newKafkaMetricsStub()
	consumer := &KafkaConsumer{
		client: client, retryInterval: time.Millisecond,
		maxAttempts: 2, deadLetterTopic: "orders.created.v1.dlq", groupID: "payments",
		handlers: map[string]MessageHandler{message.Topic: handler}, metrics: metrics,
	}

	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if handler.calls != 2 || client.commitCount != 1 {
		t.Fatalf("handler calls = %d, commits = %d; want 2 and 1", handler.calls, client.commitCount)
	}
	if client.resetCount != 0 || client.allowCount != 2 {
		t.Fatalf("offset resets = %d, rebalance allows = %d; want 0 and 2", client.resetCount, client.allowCount)
	}
	if metrics.processing["failure"] != 1 || metrics.processing["success"] != 1 ||
		metrics.retries["handler"] != 1 || metrics.commits["success"] != 1 {
		t.Fatalf("metrics = %+v; want one failure, success, handler retry, and successful commit", metrics)
	}
	if len(metrics.lags) != 2 || metrics.lags[0] != 8 || metrics.lags[1] != 7 {
		t.Fatalf("lag observations = %v, want [8 7]", metrics.lags)
	}
}

func TestKafkaConsumerReprocessesWhenOffsetCommitFails(t *testing.T) {
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	record, _ := recordFromMessage(message)
	record.Offset = 7
	fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{{
		Topic: record.Topic, Partitions: []kgo.FetchPartition{{HighWatermark: 8, Records: []*kgo.Record{record}}},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClientStub{
		fetches: []kgo.Fetches{fetches, fetches}, cancel: cancel,
		commitErrs: []error{errors.New("coordinator unavailable")},
	}
	handler := &retryingHandler{calls: 1}
	metrics := newKafkaMetricsStub()
	consumer := &KafkaConsumer{
		client: client, retryInterval: time.Millisecond,
		maxAttempts: 3, deadLetterTopic: "orders.created.v1.dlq", groupID: "payments",
		handlers: map[string]MessageHandler{message.Topic: handler}, metrics: metrics,
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
	if metrics.commits["failure"] != 1 || metrics.commits["success"] != 1 ||
		metrics.retries["offset_commit"] != 1 {
		t.Fatalf("commit metrics = %+v, retries = %+v; want one failure, success, and retry",
			metrics.commits, metrics.retries)
	}
}

type failingHandler struct {
	calls int
	err   error
}

func (h *failingHandler) Handle(_ context.Context, _ outbox.Message) error {
	h.calls++
	return h.err
}

func TestKafkaConsumerMovesPoisonRecordToDeadLetterBeforeCommit(t *testing.T) {
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"broken":true}`), time.Now(),
	)
	record, _ := recordFromMessage(message)
	record.Partition = 2
	record.Offset = 17
	fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{{
		Topic: record.Topic, Partitions: []kgo.FetchPartition{{HighWatermark: 18, Records: []*kgo.Record{record}}},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClientStub{fetches: []kgo.Fetches{fetches}, cancel: cancel}
	dlq := &syncProducerStub{}
	handler := &failingHandler{err: errors.New("invalid event schema")}
	metrics := newKafkaMetricsStub()
	consumer := &KafkaConsumer{
		client: client, dlqProducer: dlq, handlers: map[string]MessageHandler{message.Topic: handler},
		groupID: "payments", deadLetterTopic: "orders.created.v1.dlq",
		maxAttempts: 3, retryInterval: time.Millisecond, metrics: metrics,
	}

	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if handler.calls != 3 || dlq.produce != 1 || client.commitCount != 1 {
		t.Fatalf("handler calls = %d, DLQ produces = %d, commits = %d; want 3, 1, 1",
			handler.calls, dlq.produce, client.commitCount)
	}
	if dlq.record.Topic != "orders.created.v1.dlq" || string(dlq.record.Value) != string(record.Value) {
		t.Fatalf("dead letter record = %+v, want topic and original payload preserved", dlq.record)
	}
	if got, err := uniqueHeader(dlq.record.Headers, originalOffsetHeader); err != nil || got != "17" {
		t.Fatalf("dead letter original offset = %q, %v; want 17, nil", got, err)
	}
	if got, err := uniqueHeader(dlq.record.Headers, deliveryAttemptsHeader); err != nil || got != "3" {
		t.Fatalf("dead letter attempts = %q, %v; want 3, nil", got, err)
	}
	if metrics.processing["failure"] != 3 || metrics.retries["handler"] != 2 ||
		metrics.deadLetters != 1 || metrics.commits["success"] != 1 {
		t.Fatalf("metrics = %+v; want three failures, two retries, one DLQ and commit", metrics)
	}
}

func TestKafkaConsumerDoesNotCommitWhenDeadLetterPublishFails(t *testing.T) {
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"broken":true}`), time.Now(),
	)
	record, _ := recordFromMessage(message)
	fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{{
		Topic: record.Topic, Partitions: []kgo.FetchPartition{{Records: []*kgo.Record{record}}},
	}}}}
	ctx, cancel := context.WithCancel(context.Background())
	client := &consumerClientStub{fetches: []kgo.Fetches{fetches}}
	dlq := &syncProducerStub{err: errors.New("broker unavailable"), cancel: cancel}
	metrics := newKafkaMetricsStub()
	consumer := &KafkaConsumer{
		client: client, dlqProducer: dlq, handlers: map[string]MessageHandler{
			message.Topic: &failingHandler{err: errors.New("invalid event schema")},
		},
		groupID: "payments", deadLetterTopic: "orders.created.v1.dlq",
		maxAttempts: 1, retryInterval: time.Millisecond, metrics: metrics,
	}

	if err := consumer.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if client.commitCount != 0 || client.resetCount != 1 {
		t.Fatalf("commits = %d, offset resets = %d; want 0 and 1", client.commitCount, client.resetCount)
	}
	if metrics.deadLetters != 0 || metrics.retries["dead_letter_publish"] != 1 || len(metrics.commits) != 0 {
		t.Fatalf("metrics = %+v; want one DLQ publish retry and no DLQ success or offset commit", metrics)
	}
}

func TestFetchHighWatermarkSelectsRecordPartition(t *testing.T) {
	record := &kgo.Record{Topic: "orders.created.v1", Partition: 2}
	fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{
		{Topic: record.Topic, Partitions: []kgo.FetchPartition{
			{Partition: 1, HighWatermark: 9},
			{Partition: 2, HighWatermark: 21},
		}},
	}}}
	if got := fetchHighWatermark(fetches, record); got != 21 {
		t.Fatalf("fetchHighWatermark() = %d, want 21", got)
	}
}

func TestDeleteConsumerLagMetricsRemovesEveryRevokedPartition(t *testing.T) {
	metrics := newKafkaMetricsStub()
	deleteConsumerLagMetrics(metrics, "payments", map[string][]int32{
		"orders.created.v1": {0, 2},
	})
	if len(metrics.deletedLags) != 2 || metrics.deletedLags[0] != 0 || metrics.deletedLags[1] != 2 {
		t.Fatalf("deleted lag partitions = %v, want [0 2]", metrics.deletedLags)
	}
}
