// Package catalogapp 编排商品目录相关的应用用例。
package catalogapp

import (
	"context"
	"fmt"

	"example.com/order-system/internal/domain/catalog"
)

// Repository 是商品查询用例需要的数据能力。
//
// 接口定义在使用方应用层，内存或 PostgreSQL 适配器都可以实现它。
type Repository interface {
	List(context.Context) ([]catalog.Product, error)
	GetByID(context.Context, string) (catalog.Product, error)
}

// Service 提供商品目录用例。它只依赖 Repository 接口，
// 不知道数据实际来自内存、PostgreSQL 还是远程服务。
type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) ListProducts(ctx context.Context) ([]catalog.Product, error) {
	products, err := s.repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}

	return products, nil
}

func (s *Service) GetProduct(ctx context.Context, productID string) (catalog.Product, error) {
	product, err := s.repository.GetByID(ctx, productID)
	if err != nil {
		return catalog.Product{}, fmt.Errorf("get product %q: %w", productID, err)
	}

	return product, nil
}
