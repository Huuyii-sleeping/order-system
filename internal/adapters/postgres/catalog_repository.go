package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"example.com/order-system/internal/domain/catalog"
)

type CatalogRepository struct {
	db *sql.DB
}

func NewCatalogRepository(db *sql.DB) *CatalogRepository {
	return &CatalogRepository{db: db}
}

func (r *CatalogRepository) List(ctx context.Context) ([]catalog.Product, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, price_cents
		FROM products
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("query products: %w", err)
	}
	defer rows.Close()

	products := make([]catalog.Product, 0)
	for rows.Next() {
		product, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate products: %w", err)
	}

	return products, nil
}

func (r *CatalogRepository) GetByID(ctx context.Context, productID string) (catalog.Product, error) {
	product, err := scanProduct(r.db.QueryRowContext(ctx, `
		SELECT id, name, price_cents
		FROM products
		WHERE id = $1
	`, productID))
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Product{}, catalog.ErrProductNotFound
	}
	if err != nil {
		return catalog.Product{}, err
	}

	return product, nil
}

type productScanner interface {
	Scan(...any) error
}

func scanProduct(scanner productScanner) (catalog.Product, error) {
	var id string
	var name string
	var priceCents int64
	if err := scanner.Scan(&id, &name, &priceCents); err != nil {
		return catalog.Product{}, fmt.Errorf("scan product: %w", err)
	}

	product, err := catalog.NewProduct(id, name, priceCents)
	if err != nil {
		return catalog.Product{}, fmt.Errorf("restore product: %w", err)
	}

	return product, nil
}
