// Package order 定义订单模型、状态和领域规则。
package order

import (
	"errors"
	"math"
	"strings"
	"time"
)

var (
	ErrInvalidOrderID        = errors.New("order ID is required")
	ErrInvalidCustomerID     = errors.New("customer ID is required")
	ErrInvalidProductID      = errors.New("order product ID is required")
	ErrInvalidQuantity       = errors.New("order quantity must be greater than zero")
	ErrInvalidUnitPrice      = errors.New("unit price must be greater than zero")
	ErrInvalidIdempotencyKey = errors.New("idempotency key is required")
	ErrInvalidCreatedAt      = errors.New("order creation time is required")
	ErrOrderTotalOverflow    = errors.New("order total exceeds supported range")
	ErrOrderNotFound         = errors.New("order not found")
)

// Status 表示订单当前所在的生命周期状态。
type Status string

const (
	StatusPending   Status = "pending"
	StatusConfirmed Status = "confirmed"
	StatusCancelled Status = "cancelled"
)

// Order 是订单聚合。金额在创建时基于商品单价形成快照，
// 后续商品价格变化不会影响已经创建的订单。
type Order struct {
	id             string
	customerID     string
	productID      string
	quantity       int
	unitPriceCents int64
	totalCents     int64
	status         Status
	idempotencyKey string
	createdAt      time.Time
}

func New(
	id string,
	customerID string,
	productID string,
	quantity int,
	unitPriceCents int64,
	idempotencyKey string,
	createdAt time.Time,
) (Order, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Order{}, ErrInvalidOrderID
	}

	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return Order{}, ErrInvalidCustomerID
	}

	productID = strings.TrimSpace(productID)
	if productID == "" {
		return Order{}, ErrInvalidProductID
	}

	if quantity <= 0 {
		return Order{}, ErrInvalidQuantity
	}

	if unitPriceCents <= 0 {
		return Order{}, ErrInvalidUnitPrice
	}

	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return Order{}, ErrInvalidIdempotencyKey
	}

	if createdAt.IsZero() {
		return Order{}, ErrInvalidCreatedAt
	}

	if unitPriceCents > math.MaxInt64/int64(quantity) {
		return Order{}, ErrOrderTotalOverflow
	}

	return Order{
		id:             id,
		customerID:     customerID,
		productID:      productID,
		quantity:       quantity,
		unitPriceCents: unitPriceCents,
		totalCents:     unitPriceCents * int64(quantity),
		status:         StatusPending,
		idempotencyKey: idempotencyKey,
		createdAt:      createdAt.UTC(),
	}, nil
}

func (o Order) ID() string             { return o.id }
func (o Order) CustomerID() string     { return o.customerID }
func (o Order) ProductID() string      { return o.productID }
func (o Order) Quantity() int          { return o.quantity }
func (o Order) UnitPriceCents() int64  { return o.unitPriceCents }
func (o Order) TotalCents() int64      { return o.totalCents }
func (o Order) Status() Status         { return o.status }
func (o Order) IdempotencyKey() string { return o.idempotencyKey }
func (o Order) CreatedAt() time.Time   { return o.createdAt }
