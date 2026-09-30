// Package inventory 定义库存模型和库存变化规则。
package inventory

import (
	"errors"
	"math"
	"strings"
)

var (
	ErrInvalidProductID  = errors.New("inventory product ID is required")
	ErrInvalidStock      = errors.New("available stock cannot be negative")
	ErrInvalidQuantity   = errors.New("quantity must be greater than zero")
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrStockOverflow     = errors.New("available stock exceeds supported range")
	ErrStockNotFound     = errors.New("stock not found")
)

// Stock 表示一个商品当前可用的库存。
//
// 字段保持私有，库存只能通过 Reserve 等领域方法变化，
// 防止调用方绕过规则直接把库存改成负数。
type Stock struct {
	productID string
	available int
}

func NewStock(productID string, available int) (Stock, error) {
	productID = strings.TrimSpace(productID)
	if productID == "" {
		return Stock{}, ErrInvalidProductID
	}

	if available < 0 {
		return Stock{}, ErrInvalidStock
	}

	return Stock{productID: productID, available: available}, nil
}

// Reserve 预占指定数量的库存，并返回变化后的新值。
//
// Stock 使用值语义：校验失败时原值不会被部分修改；
// 调用方必须显式保存返回的新 Stock，状态变化才会生效。
func (s Stock) Reserve(quantity int) (Stock, error) {
	if quantity <= 0 {
		return Stock{}, ErrInvalidQuantity
	}

	if quantity > s.available {
		return Stock{}, ErrInsufficientStock
	}

	s.available -= quantity
	return s, nil
}

// Release 释放之前为订单预占的库存。它只负责一次合法的库存加回；
// “同一订单是否已经补偿过”由订单状态机和消息 Inbox 共同保证。
func (s Stock) Release(quantity int) (Stock, error) {
	if quantity <= 0 {
		return Stock{}, ErrInvalidQuantity
	}
	if quantity > math.MaxInt-s.available {
		return Stock{}, ErrStockOverflow
	}

	s.available += quantity
	return s, nil
}

func (s Stock) ProductID() string { return s.productID }

func (s Stock) Available() int { return s.available }
