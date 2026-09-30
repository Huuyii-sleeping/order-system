package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"example.com/order-system/internal/application/inbox"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/domain/inventory"
	"example.com/order-system/internal/domain/order"
)

const (
	idempotencyKeyConstraint  = "orders_customer_idempotency_key_unique"
	orderPrimaryKeyConstraint = "orders_pkey"
)

var ErrDuplicateOrderID = errors.New("duplicate order ID")

type OrderStore struct {
	db *sql.DB
}

func NewOrderStore(db *sql.DB) *OrderStore {
	return &OrderStore{db: db}
}

func (s *OrderStore) Transact(ctx context.Context, fn func(orderapp.Transaction) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Commit 后 Rollback 返回 sql.ErrTxDone；失败路径和 panic 路径则会
	// 真正回滚。这样回调无论怎样退出都不会遗留悬挂事务。
	defer func() { _ = tx.Rollback() }()

	if err := fn(&orderTransaction{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (s *OrderStore) TransactPaymentResult(
	ctx context.Context, fn func(orderapp.PaymentResultTransaction) error,
) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin payment result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&orderTransaction{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit payment result transaction: %w", err)
	}
	return nil
}

func (s *OrderStore) GetOrderByIDAndCustomerID(
	ctx context.Context,
	orderID, customerID string,
) (order.Order, error) {
	got, err := scanOrder(s.db.QueryRowContext(
		ctx,
		orderSelect+` WHERE id = $1 AND customer_id = $2`,
		orderID,
		customerID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, order.ErrOrderNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("get order by ID: %w", err)
	}

	return got, nil
}

type orderTransaction struct {
	ctx context.Context
	tx  *sql.Tx
}

func (tx *orderTransaction) TryBeginMessage(
	messageID, topic, payloadHash string, processedAt time.Time,
) (bool, error) {
	result, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO inbox_messages (message_id, topic, payload_hash, processed_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (message_id) DO NOTHING
	`, messageID, topic, payloadHash, processedAt)
	if err != nil {
		return false, fmt.Errorf("insert inbox message: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read inserted inbox rows: %w", err)
	}
	if rows == 1 {
		return true, nil
	}
	var existingTopic, existingPayloadHash string
	if err := tx.tx.QueryRowContext(tx.ctx, `
		SELECT topic, payload_hash FROM inbox_messages WHERE message_id = $1
	`, messageID).Scan(&existingTopic, &existingPayloadHash); err != nil {
		return false, fmt.Errorf("read duplicate inbox message: %w", err)
	}
	if existingTopic != topic || existingPayloadHash != payloadHash {
		return false, inbox.ErrMessageConflict
	}
	return false, nil
}

func (tx *orderTransaction) GetOrderForUpdate(orderID string) (order.Order, error) {
	got, err := scanOrder(tx.tx.QueryRowContext(
		tx.ctx, orderSelect+` WHERE id = $1 FOR UPDATE`, strings.TrimSpace(orderID),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, order.ErrOrderNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("get order for update: %w", err)
	}
	return got, nil
}

func (tx *orderTransaction) UpdateOrder(updated order.Order) error {
	result, err := tx.tx.ExecContext(tx.ctx, `
		UPDATE orders SET status = $1 WHERE id = $2
	`, updated.Status(), updated.ID())
	if err != nil {
		return fmt.Errorf("update order: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated order rows: %w", err)
	}
	if rows != 1 {
		return order.ErrOrderNotFound
	}
	return nil
}

func (tx *orderTransaction) GetOrderByIdempotencyKey(customerID, key string) (order.Order, error) {
	// 相同幂等键在所有服务实例间串行执行。事务结束时 PostgreSQL
	// 自动释放 advisory lock，不需要显式解锁。
	if _, err := tx.tx.ExecContext(tx.ctx, `
		SELECT pg_advisory_xact_lock(
			hashtextextended($2, hashtextextended($1, 0))
		)
	`, customerID, key); err != nil {
		return order.Order{}, fmt.Errorf("lock idempotency key: %w", err)
	}

	got, err := scanOrder(tx.tx.QueryRowContext(
		tx.ctx,
		orderSelect+` WHERE customer_id = $1 AND idempotency_key = $2`,
		customerID, key,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return order.Order{}, order.ErrOrderNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("get order by idempotency key: %w", err)
	}

	return got, nil
}

func (tx *orderTransaction) GetStock(productID string) (inventory.Stock, error) {
	var available int
	err := tx.tx.QueryRowContext(tx.ctx, `
		SELECT available
		FROM stocks
		WHERE product_id = $1
		FOR UPDATE
	`, productID).Scan(&available)
	if errors.Is(err, sql.ErrNoRows) {
		return inventory.Stock{}, inventory.ErrStockNotFound
	}
	if err != nil {
		return inventory.Stock{}, fmt.Errorf("select stock for update: %w", err)
	}

	stock, err := inventory.NewStock(productID, available)
	if err != nil {
		return inventory.Stock{}, fmt.Errorf("restore stock: %w", err)
	}

	return stock, nil
}

func (tx *orderTransaction) SaveStock(stock inventory.Stock) error {
	result, err := tx.tx.ExecContext(tx.ctx, `
		UPDATE stocks
		SET available = $1, updated_at = NOW()
		WHERE product_id = $2
	`, stock.Available(), stock.ProductID())
	if err != nil {
		return fmt.Errorf("update stock: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated stock rows: %w", err)
	}
	if rowsAffected != 1 {
		return inventory.ErrStockNotFound
	}

	return nil
}

func (tx *orderTransaction) SaveOrder(created order.Order) error {
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO orders (
			id, customer_id, product_id, quantity, unit_price_cents,
			total_cents, status, idempotency_key, created_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`,
		created.ID(),
		created.CustomerID(),
		created.ProductID(),
		created.Quantity(),
		created.UnitPriceCents(),
		created.TotalCents(),
		created.Status(),
		created.IdempotencyKey(),
		created.CreatedAt(),
	)
	if err == nil {
		return nil
	}

	return classifyInsertOrderError(err)
}

func (tx *orderTransaction) SaveOutboxMessage(message outbox.Message) error {
	_, err := tx.tx.ExecContext(tx.ctx, `
		INSERT INTO outbox_messages (
			id, topic, message_key, payload, occurred_at, next_attempt_at
		) VALUES ($1, $2, $3, $4, $5, $5)
	`, message.ID, message.Topic, message.Key, []byte(message.Payload), message.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert outbox message: %w", err)
	}
	return nil
}

func classifyInsertOrderError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		switch postgresError.ConstraintName {
		case idempotencyKeyConstraint:
			return orderapp.ErrIdempotencyConflict
		case orderPrimaryKeyConstraint:
			return ErrDuplicateOrderID
		}
	}

	return fmt.Errorf("insert order: %w", err)
}

const orderSelect = `
	SELECT
		id, customer_id, product_id, quantity, unit_price_cents,
		total_cents, status, idempotency_key, created_at
	FROM orders
`

type orderScanner interface {
	Scan(...any) error
}

func scanOrder(scanner orderScanner) (order.Order, error) {
	var record struct {
		id             string
		customerID     string
		productID      string
		quantity       int
		unitPriceCents int64
		totalCents     int64
		status         string
		idempotencyKey string
		createdAt      sql.NullTime
	}

	err := scanner.Scan(
		&record.id,
		&record.customerID,
		&record.productID,
		&record.quantity,
		&record.unitPriceCents,
		&record.totalCents,
		&record.status,
		&record.idempotencyKey,
		&record.createdAt,
	)
	if err != nil {
		return order.Order{}, err
	}
	if !record.createdAt.Valid {
		return order.Order{}, order.ErrInvalidCreatedAt
	}

	restored, err := order.Restore(
		record.id,
		record.customerID,
		record.productID,
		record.quantity,
		record.unitPriceCents,
		record.totalCents,
		order.Status(record.status),
		record.idempotencyKey,
		record.createdAt.Time,
	)
	if err != nil {
		return order.Order{}, fmt.Errorf("restore order: %w", err)
	}

	return restored, nil
}
