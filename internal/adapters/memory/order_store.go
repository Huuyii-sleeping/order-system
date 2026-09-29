package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

var (
	ErrDuplicateStock          = errors.New("duplicate stock product ID")
	ErrDuplicateOrderID        = errors.New("duplicate order ID")
	ErrDuplicateIdempotencyKey = errors.New("duplicate idempotency key")
)

// OrderStore 把订单和库存保存在进程内存中。
//
// mutex 使每个 Transact 回调串行执行，从而保证“检查库存 + 扣库存 +
// 保存订单”不会被另一个并发请求插入。数据库版本会用数据库事务和行锁
// 提供同样的保证。
type OrderStore struct {
	mu sync.RWMutex

	stocksByProduct         map[string]inventory.Stock
	ordersByID              map[string]order.Order
	orderIDByIdempotencyKey map[idempotencyScope]string
	outboxMessages          map[string]memoryOutboxMessage
}

func NewOrderStore(stocks []inventory.Stock) (*OrderStore, error) {
	stocksByProduct := make(map[string]inventory.Stock, len(stocks))
	for _, stock := range stocks {
		if _, exists := stocksByProduct[stock.ProductID()]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateStock, stock.ProductID())
		}

		stocksByProduct[stock.ProductID()] = stock
	}

	return &OrderStore{
		stocksByProduct:         stocksByProduct,
		ordersByID:              make(map[string]order.Order),
		orderIDByIdempotencyKey: make(map[idempotencyScope]string),
		outboxMessages:          make(map[string]memoryOutboxMessage),
	}, nil
}

func (s *OrderStore) Transact(ctx context.Context, fn func(orderapp.Transaction) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return err
	}

	// Copy-on-write 模拟数据库事务：回调只能修改副本。
	// 只有回调成功，副本才会整体替换正式状态；返回错误即自然回滚。
	tx := &memoryTransaction{
		stocksByProduct:         cloneMap(s.stocksByProduct),
		ordersByID:              cloneMap(s.ordersByID),
		orderIDByIdempotencyKey: cloneMap(s.orderIDByIdempotencyKey),
		outboxMessages:          cloneMap(s.outboxMessages),
	}

	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.stocksByProduct = tx.stocksByProduct
	s.ordersByID = tx.ordersByID
	s.orderIDByIdempotencyKey = tx.orderIDByIdempotencyKey
	s.outboxMessages = tx.outboxMessages
	return nil
}

func (s *OrderStore) GetOrderByIDAndCustomerID(
	ctx context.Context,
	orderID, customerID string,
) (order.Order, error) {
	if err := ctx.Err(); err != nil {
		return order.Order{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	got, ok := s.ordersByID[orderID]
	if !ok || got.CustomerID() != customerID {
		return order.Order{}, order.ErrOrderNotFound
	}

	return got, nil
}

type memoryTransaction struct {
	stocksByProduct         map[string]inventory.Stock
	ordersByID              map[string]order.Order
	orderIDByIdempotencyKey map[idempotencyScope]string
	outboxMessages          map[string]memoryOutboxMessage
}

type idempotencyScope struct {
	customerID string
	key        string
}

func (tx *memoryTransaction) GetOrderByIdempotencyKey(customerID, key string) (order.Order, error) {
	orderID, ok := tx.orderIDByIdempotencyKey[idempotencyScope{customerID: customerID, key: key}]
	if !ok {
		return order.Order{}, order.ErrOrderNotFound
	}

	return tx.ordersByID[orderID], nil
}

func (tx *memoryTransaction) GetStock(productID string) (inventory.Stock, error) {
	stock, ok := tx.stocksByProduct[productID]
	if !ok {
		return inventory.Stock{}, inventory.ErrStockNotFound
	}

	return stock, nil
}

func (tx *memoryTransaction) SaveStock(stock inventory.Stock) error {
	if _, ok := tx.stocksByProduct[stock.ProductID()]; !ok {
		return inventory.ErrStockNotFound
	}

	tx.stocksByProduct[stock.ProductID()] = stock
	return nil
}

func (tx *memoryTransaction) SaveOrder(created order.Order) error {
	if _, exists := tx.ordersByID[created.ID()]; exists {
		return ErrDuplicateOrderID
	}
	scope := idempotencyScope{customerID: created.CustomerID(), key: created.IdempotencyKey()}
	if _, exists := tx.orderIDByIdempotencyKey[scope]; exists {
		return ErrDuplicateIdempotencyKey
	}

	tx.ordersByID[created.ID()] = created
	tx.orderIDByIdempotencyKey[scope] = created.ID()
	return nil
}

func (tx *memoryTransaction) SaveOutboxMessage(message outbox.Message) error {
	if _, exists := tx.outboxMessages[message.ID]; exists {
		return fmt.Errorf("duplicate outbox message ID %q", message.ID)
	}
	message.Payload = append([]byte(nil), message.Payload...)
	tx.outboxMessages[message.ID] = memoryOutboxMessage{
		Message: message, nextAttemptAt: message.OccurredAt,
	}
	return nil
}

type memoryOutboxMessage struct {
	outbox.Message
	nextAttemptAt time.Time
	lockedUntil   time.Time
	publishedAt   time.Time
	lastError     string
}

func (s *OrderStore) Claim(
	ctx context.Context, leaseID string, batchSize int, now, lockedUntil time.Time,
) ([]outbox.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(leaseID) == "" || batchSize <= 0 || !lockedUntil.After(now) {
		return nil, outbox.ErrInvalidClaim
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	eligible := make([]string, 0, len(s.outboxMessages))
	for id, record := range s.outboxMessages {
		if !record.publishedAt.IsZero() || record.nextAttemptAt.After(now) ||
			(!record.lockedUntil.IsZero() && record.lockedUntil.After(now)) {
			continue
		}
		eligible = append(eligible, id)
	}
	sort.Slice(eligible, func(i, j int) bool {
		left := s.outboxMessages[eligible[i]].OccurredAt
		right := s.outboxMessages[eligible[j]].OccurredAt
		if left.Equal(right) {
			return eligible[i] < eligible[j]
		}
		return left.Before(right)
	})
	if len(eligible) > batchSize {
		eligible = eligible[:batchSize]
	}

	messages := make([]outbox.Message, 0, len(eligible))
	for _, id := range eligible {
		record := s.outboxMessages[id]
		record.Attempts++
		record.LeaseID = leaseID
		record.lockedUntil = lockedUntil
		s.outboxMessages[id] = record
		message := record.Message
		message.Payload = append([]byte(nil), message.Payload...)
		messages = append(messages, message)
	}
	return messages, nil
}

func (s *OrderStore) MarkPublished(
	ctx context.Context, messageID, leaseID string, publishedAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.outboxMessages[messageID]
	if !exists || record.LeaseID != leaseID || !record.publishedAt.IsZero() {
		return outbox.ErrLeaseLost
	}
	record.publishedAt = publishedAt
	record.LeaseID = ""
	record.lockedUntil = time.Time{}
	record.lastError = ""
	s.outboxMessages[messageID] = record
	return nil
}

func (s *OrderStore) MarkFailed(
	ctx context.Context, messageID, leaseID string, nextAttemptAt time.Time, reason string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, exists := s.outboxMessages[messageID]
	if !exists || record.LeaseID != leaseID || !record.publishedAt.IsZero() {
		return outbox.ErrLeaseLost
	}
	record.nextAttemptAt = nextAttemptAt
	record.lastError = reason
	record.LeaseID = ""
	record.lockedUntil = time.Time{}
	s.outboxMessages[messageID] = record
	return nil
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	cloned := make(map[K]V, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
