package memory

import (
	"time"

	"example.com/order-system/internal/application/outbox"
)

// inboxMessage 和 memoryOutboxMessage 是订单、支付两个内存适配器共用的
// 持久化记录。它们模拟 PostgreSQL 中的 inbox_messages 和 outbox_messages。
type inboxMessage struct {
	topic       string
	payloadHash string
	processedAt time.Time
}

type memoryOutboxMessage struct {
	outbox.Message
	nextAttemptAt time.Time
	lockedUntil   time.Time
	publishedAt   time.Time
	lastError     string
}
