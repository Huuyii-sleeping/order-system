package messaging

import (
	"context"
	"testing"
	"time"

	"example.com/order-system/internal/application/messageops"
	"example.com/order-system/internal/application/outbox"
)

func TestDecodeDeadLetterForReplayRemovesFailureMetadataAndPreservesEnvelope(t *testing.T) {
	message, err := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1",
		[]byte(`{"order_id":"order-1"}`), time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	original, err := recordFromMessage(message)
	if err != nil {
		t.Fatalf("recordFromMessage() error = %v", err)
	}
	original.Partition, original.Offset = 1, 25
	deadLetter, err := deadLetterRecord(
		original, "orders.created.v1.dlq", "payments-v1", 3,
		context.DeadlineExceeded, time.Now(),
	)
	if err != nil {
		t.Fatalf("deadLetterRecord() error = %v", err)
	}
	deadLetter.Partition, deadLetter.Offset = 2, 80

	decoded, err := decodeDeadLetterForReplay(deadLetter)
	if err != nil {
		t.Fatalf("decodeDeadLetterForReplay() error = %v", err)
	}
	if decoded.OriginalTopic != message.Topic || decoded.Target() != "orders.created.v1.dlq/2/80" {
		t.Fatalf("decoded dead letter = %+v", decoded)
	}
	for _, header := range decoded.Headers {
		if len(header.Key) >= len(deadLetterHeaderPrefix) && header.Key[:len(deadLetterHeaderPrefix)] == deadLetterHeaderPrefix {
			t.Fatalf("replay retained dead-letter header %q", header.Key)
		}
	}
}

func TestReplayDeadLetterValidatesEnvelopeBeforePublishing(t *testing.T) {
	producer := &syncProducerStub{}
	replayer := &DeadLetterReplayer{producer: producer}
	err := replayer.ReplayDeadLetter(context.Background(), messageops.DeadLetterMessage{
		OriginalTopic: "orders.created.v1", Value: []byte(`{"order_id":"order-1"}`),
	})
	if err == nil || producer.produce != 0 {
		t.Fatalf("ReplayDeadLetter() error = %v, produce calls = %d", err, producer.produce)
	}
}

func TestReplayDeadLetterPublishesOriginalTopic(t *testing.T) {
	producer := &syncProducerStub{}
	replayer := &DeadLetterReplayer{producer: producer}
	message, _ := outbox.NewMessage(
		"message-1", "orders.created.v1", "order-1", []byte(`{"ok":true}`), time.Now(),
	)
	record, _ := recordFromMessage(message)
	headers := make([]messageops.Header, 0, len(record.Headers))
	for _, header := range record.Headers {
		headers = append(headers, messageops.Header{Key: header.Key, Value: header.Value})
	}
	err := replayer.ReplayDeadLetter(context.Background(), messageops.DeadLetterMessage{
		OriginalTopic: message.Topic, Key: record.Key, Value: record.Value, Headers: headers,
	})
	if err != nil {
		t.Fatalf("ReplayDeadLetter() error = %v", err)
	}
	if producer.record.Topic != message.Topic || producer.produce != 1 {
		t.Fatalf("produced record = %+v, calls = %d", producer.record, producer.produce)
	}
}
