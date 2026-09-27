// Package memory 提供基于进程内存的 Repository 实现。
//
// 它适合当前学习阶段和测试；进程重启后数据会丢失。
package memory

import (
	"context"

	"example.com/order-system/internal/domain/catalog"
)

// CatalogRepository 是只读的内存商品仓库。
type CatalogRepository struct {
	products    []catalog.Product
	productByID map[string]catalog.Product
}

func NewCatalogRepository(products []catalog.Product) *CatalogRepository {
	productCopy := append([]catalog.Product(nil), products...)
	productByID := make(map[string]catalog.Product, len(productCopy))

	for _, product := range productCopy {
		productByID[product.ID()] = product
	}

	return &CatalogRepository{products: productCopy, productByID: productByID}
}

func (r *CatalogRepository) List(ctx context.Context) ([]catalog.Product, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// 返回切片副本，避免调用方修改仓库内部的切片结构。
	return append([]catalog.Product(nil), r.products...), nil
}

func (r *CatalogRepository) GetByID(ctx context.Context, productID string) (catalog.Product, error) {
	if err := ctx.Err(); err != nil {
		return catalog.Product{}, err
	}

	product, ok := r.productByID[productID]
	if !ok {
		return catalog.Product{}, catalog.ErrProductNotFound
	}

	return product, nil
}
