package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	orderapp "example.com/order-system/internal/application/order"
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
	orderIDByIdempotencyKey map[string]string
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
		orderIDByIdempotencyKey: make(map[string]string),
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
	return nil
}

func (s *OrderStore) GetOrderByID(ctx context.Context, orderID string) (order.Order, error) {
	if err := ctx.Err(); err != nil {
		return order.Order{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	got, ok := s.ordersByID[orderID]
	if !ok {
		return order.Order{}, order.ErrOrderNotFound
	}

	return got, nil
}

type memoryTransaction struct {
	stocksByProduct         map[string]inventory.Stock
	ordersByID              map[string]order.Order
	orderIDByIdempotencyKey map[string]string
}

func (tx *memoryTransaction) GetOrderByIdempotencyKey(key string) (order.Order, error) {
	orderID, ok := tx.orderIDByIdempotencyKey[key]
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
	if _, exists := tx.orderIDByIdempotencyKey[created.IdempotencyKey()]; exists {
		return ErrDuplicateIdempotencyKey
	}

	tx.ordersByID[created.ID()] = created
	tx.orderIDByIdempotencyKey[created.IdempotencyKey()] = created.ID()
	return nil
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	cloned := make(map[K]V, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
