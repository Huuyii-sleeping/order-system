// Package orderapp 编排订单相关的应用用例。
package orderapp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/order-system/internal/domain/catalog"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

var ErrIdempotencyConflict = errors.New("idempotency key was already used for another order request")

// ProductCatalog 是下单用例读取商品快照所需的最小能力。
type ProductCatalog interface {
	GetByID(context.Context, string) (catalog.Product, error)
}

// Transaction 描述一次下单事务内可以进行的操作。
//
// 应用层定义事务边界，适配器负责用互斥锁或数据库事务实现原子性。
type Transaction interface {
	GetOrderByIdempotencyKey(string, string) (order.Order, error)
	GetStock(string) (inventory.Stock, error)
	SaveStock(inventory.Stock) error
	SaveOrder(order.Order) error
}

// Store 同时提供订单查询和事务能力。
type Store interface {
	Transact(context.Context, func(Transaction) error) error
	GetOrderByIDAndCustomerID(context.Context, string, string) (order.Order, error)
}

type IDGenerator interface {
	NewID() (string, error)
}

type Clock interface {
	Now() time.Time
}

type Service struct {
	products ProductCatalog
	store    Store
	ids      IDGenerator
	clock    Clock
}

func NewService(products ProductCatalog, store Store, ids IDGenerator, clock Clock) *Service {
	return &Service{products: products, store: store, ids: ids, clock: clock}
}

type CreateOrderCommand struct {
	CustomerID     string
	ProductID      string
	Quantity       int
	IdempotencyKey string
}

type CreateOrderResult struct {
	Order   order.Order
	Created bool
}

// CreateOrder 把多个领域对象和端口编排成一个完整用例：
// 检查幂等键、生成订单、预占库存，并原子地保存库存和订单。
func (s *Service) CreateOrder(ctx context.Context, command CreateOrderCommand) (CreateOrderResult, error) {
	command, err := normalizeCreateOrderCommand(command)
	if err != nil {
		return CreateOrderResult{}, err
	}

	// 外部查询放在事务之前，避免数据库事务持锁期间等待外部服务。
	product, err := s.products.GetByID(ctx, command.ProductID)
	if err != nil {
		return CreateOrderResult{}, fmt.Errorf("get product %q: %w", command.ProductID, err)
	}

	var result CreateOrderResult
	err = s.store.Transact(ctx, func(tx Transaction) error {
		existing, err := tx.GetOrderByIdempotencyKey(command.CustomerID, command.IdempotencyKey)
		switch {
		case err == nil:
			// 相同幂等键和相同请求返回第一次的结果，不会重复扣库存。
			if !sameRequest(existing, command) {
				return ErrIdempotencyConflict
			}

			result = CreateOrderResult{Order: existing, Created: false}
			return nil
		case !errors.Is(err, order.ErrOrderNotFound):
			return fmt.Errorf("find order by idempotency key: %w", err)
		}

		orderID, err := s.ids.NewID()
		if err != nil {
			return fmt.Errorf("generate order ID: %w", err)
		}

		created, err := order.New(
			orderID,
			command.CustomerID,
			command.ProductID,
			command.Quantity,
			product.PriceCents(),
			command.IdempotencyKey,
			s.clock.Now(),
		)
		if err != nil {
			return fmt.Errorf("create order: %w", err)
		}

		stock, err := tx.GetStock(command.ProductID)
		if err != nil {
			return fmt.Errorf("get stock: %w", err)
		}

		reserved, err := stock.Reserve(command.Quantity)
		if err != nil {
			return fmt.Errorf("reserve stock: %w", err)
		}

		// 两次保存都处于同一个事务中。后一步失败时，适配器必须
		// 回滚前面暂存的库存变化，避免出现“扣了库存却没有订单”。
		if err := tx.SaveStock(reserved); err != nil {
			return fmt.Errorf("save stock: %w", err)
		}
		if err := tx.SaveOrder(created); err != nil {
			return fmt.Errorf("save order: %w", err)
		}

		result = CreateOrderResult{Order: created, Created: true}
		return nil
	})
	if err != nil {
		return CreateOrderResult{}, fmt.Errorf("create order transaction: %w", err)
	}

	return result, nil
}

func (s *Service) GetOrder(ctx context.Context, customerID, orderID string) (order.Order, error) {
	got, err := s.store.GetOrderByIDAndCustomerID(
		ctx,
		strings.TrimSpace(orderID),
		strings.TrimSpace(customerID),
	)
	if err != nil {
		return order.Order{}, fmt.Errorf("get order %q: %w", orderID, err)
	}

	return got, nil
}

func normalizeCreateOrderCommand(command CreateOrderCommand) (CreateOrderCommand, error) {
	command.CustomerID = strings.TrimSpace(command.CustomerID)
	command.ProductID = strings.TrimSpace(command.ProductID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)

	switch {
	case command.CustomerID == "":
		return CreateOrderCommand{}, order.ErrInvalidCustomerID
	case command.ProductID == "":
		return CreateOrderCommand{}, order.ErrInvalidProductID
	case command.Quantity <= 0:
		return CreateOrderCommand{}, order.ErrInvalidQuantity
	case command.IdempotencyKey == "":
		return CreateOrderCommand{}, order.ErrInvalidIdempotencyKey
	default:
		return command, nil
	}
}

func sameRequest(existing order.Order, command CreateOrderCommand) bool {
	return existing.CustomerID() == command.CustomerID &&
		existing.ProductID() == command.ProductID &&
		existing.Quantity() == command.Quantity
}
