package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/migrations"
)

// TestIntegrationPostgresOrderListPlanUsesCompositeIndex 验证订单列表的查询形状
// 在更接近真实数据量的情况下仍能匹配租户、排序和游标边界对应的复合索引。
// 这里检查执行计划而不是硬编码耗时：本地容器的 CPU、磁盘和缓存状态不稳定，
// 但 Seq Scan 是否出现、目标索引是否被使用，才是这个查询的结构性约束。
func TestIntegrationPostgresOrderListPlanUsesCompositeIndex(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL query-plan integration tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	schema := fmt.Sprintf("order_system_query_plan_%d", time.Now().UnixNano())
	adminDB, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	if _, err := adminDB.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create query-plan schema: %v", err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := adminDB.ExecContext(dropCtx, `DROP SCHEMA "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop query-plan schema: %v", err)
		}
	})

	testDatabaseURL := withSearchPath(t, databaseURL, schema)
	db, err := postgres.Open(ctx, testDatabaseURL)
	if err != nil {
		t.Fatalf("connect query-plan database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := postgres.Migrate(ctx, db, migrations.Files); err != nil {
		t.Fatalf("migrate query-plan database: %v", err)
	}
	seedIntegrationUsers(t, db, "query-plan-customer")

	const rows = 5000
	_, err = db.ExecContext(ctx, `
		INSERT INTO orders (
			id, customer_id, product_id, quantity, unit_price_cents,
			total_cents, status, idempotency_key, created_at
		)
		SELECT
			'query-plan-order-' || lpad(sequence::text, 5, '0'),
			'query-plan-customer', 'product-2', 1, 6900, 6900,
			'pending', 'query-plan-key-' || lpad(sequence::text, 5, '0'),
			TIMESTAMPTZ '2026-01-01 00:00:00+00' - (sequence * INTERVAL '1 second')
		FROM generate_series(1, $1) AS generated(sequence)
	`, rows)
	if err != nil {
		t.Fatalf("seed query-plan orders: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ANALYZE orders`); err != nil {
		t.Fatalf("analyze query-plan orders: %v", err)
	}

	plan, err := explainOrderListQuery(ctx, db)
	if err != nil {
		t.Fatalf("explain order list query: %v", err)
	}
	if !plan.containsIndex("orders_customer_created_at_id_idx") {
		t.Fatalf("order list query did not use the composite index:\n%s", plan.pretty())
	}
	if plan.containsNode("Seq Scan") {
		t.Fatalf("order list query regressed to a sequential scan:\n%s", plan.pretty())
	}
}

type explainDocument []struct {
	Plan explainNode `json:"Plan"`
}

type explainNode struct {
	NodeType  string        `json:"Node Type"`
	IndexName string        `json:"Index Name"`
	Plans     []explainNode `json:"Plans"`
}

func explainOrderListQuery(ctx context.Context, db *sql.DB) (explainDocument, error) {
	rows, err := db.QueryContext(ctx, `
		EXPLAIN (FORMAT JSON, COSTS OFF)
		SELECT id, customer_id, product_id, quantity, unit_price_cents,
		       total_cents, status, idempotency_key, created_at
		FROM orders
		WHERE customer_id = $1
		  AND (created_at, id) < ($2, $3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`,
		"query-plan-customer",
		time.Date(2025, 12, 31, 23, 20, 0, 0, time.UTC),
		"query-plan-order-02500",
		50,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("EXPLAIN returned no plan")
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var plan explainDocument
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("decode JSON plan: %w", err)
	}
	return plan, nil
}

func (p explainDocument) containsIndex(indexName string) bool {
	for _, root := range p {
		if root.Plan.containsIndex(indexName) {
			return true
		}
	}
	return false
}

func (p explainDocument) containsNode(nodeType string) bool {
	for _, root := range p {
		if root.Plan.containsNode(nodeType) {
			return true
		}
	}
	return false
}

func (p explainDocument) pretty() string {
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Sprintf("<unprintable plan: %v>", err)
	}
	return string(raw)
}

func (n explainNode) containsIndex(indexName string) bool {
	if n.IndexName == indexName {
		return true
	}
	for _, child := range n.Plans {
		if child.containsIndex(indexName) {
			return true
		}
	}
	return false
}

func (n explainNode) containsNode(nodeType string) bool {
	if n.NodeType == nodeType {
		return true
	}
	for _, child := range n.Plans {
		if child.containsNode(nodeType) {
			return true
		}
	}
	return false
}
