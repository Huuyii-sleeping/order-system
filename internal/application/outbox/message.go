// Package outbox 定义可靠异步消息的应用契约。
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidMessage = errors.New("invalid outbox message")
	ErrInvalidClaim   = errors.New("invalid outbox claim")
	ErrLeaseLost      = errors.New("outbox message lease was lost")
)

// Message 是写入 Outbox 的稳定事件信封。Attempts 和 LeaseID 由存储在认领时填充。
type Message struct {
	ID         string
	Topic      string
	Key        string
	Payload    json.RawMessage
	OccurredAt time.Time
	Attempts   int
	LeaseID    string
}

func NewMessage(
	id string,
	topic string,
	key string,
	payload []byte,
	occurredAt time.Time,
) (Message, error) {
	id = strings.TrimSpace(id)
	topic = strings.TrimSpace(topic)
	key = strings.TrimSpace(key)
	if id == "" || topic == "" || key == "" ||
		!json.Valid(payload) || occurredAt.IsZero() {
		return Message{}, ErrInvalidMessage
	}

	return Message{
		ID: id, Topic: topic, Key: key,
		Payload:    append(json.RawMessage(nil), payload...),
		OccurredAt: occurredAt.UTC(),
	}, nil
}

// Store 同时支持 Worker 认领消息和按租约确认结果。
// LeaseID 防止超时 Worker 覆盖另一个 Worker 的处理结果。
type Store interface {
	Claim(
		ctx context.Context,
		leaseID string,
		batchSize int,
		now time.Time,
		lockedUntil time.Time,
	) ([]Message, error)
	MarkPublished(ctx context.Context, messageID, leaseID string, publishedAt time.Time) error
	MarkFailed(ctx context.Context, messageID, leaseID string, nextAttemptAt time.Time, reason string) error
}

type Publisher interface {
	Publish(context.Context, Message) error
}
