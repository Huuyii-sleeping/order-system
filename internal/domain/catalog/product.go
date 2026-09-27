// Package catalog 定义商品目录的核心业务模型和规则。
//
// 领域包不依赖 HTTP、数据库或配置，确保业务概念可以被不同入口复用。
package catalog

import (
	"errors"
	"strings"
)

var (
	ErrInvalidProductID    = errors.New("product ID is required")
	ErrInvalidProductName  = errors.New("product name is required")
	ErrInvalidProductPrice = errors.New("product price must be greater than zero")
	ErrProductNotFound     = errors.New("product not found")
)

// Product 是商品目录中的商品。字段保持私有，外部只能通过构造函数
// 创建合法商品，并通过只读方法获取数据。
type Product struct {
	id         string
	name       string
	priceCents int64
}

// NewProduct 创建商品并维护领域不变量。
//
// 金额使用最小货币单位“分”的整数表示，避免 float64 的精度问题。
func NewProduct(id, name string, priceCents int64) (Product, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Product{}, ErrInvalidProductID
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return Product{}, ErrInvalidProductName
	}

	if priceCents <= 0 {
		return Product{}, ErrInvalidProductPrice
	}

	return Product{id: id, name: name, priceCents: priceCents}, nil
}

func (p Product) ID() string { return p.id }

func (p Product) Name() string { return p.name }

func (p Product) PriceCents() int64 { return p.priceCents }
