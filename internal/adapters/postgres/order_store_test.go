package postgres

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/domain/order"
)

type scannerStub struct {
	values []any
	err    error
}

func (s scannerStub) Scan(destinations ...any) error {
	if s.err != nil {
		return s.err
	}

	*(destinations[0].(*string)) = s.values[0].(string)
	*(destinations[1].(*string)) = s.values[1].(string)
	*(destinations[2].(*string)) = s.values[2].(string)
	*(destinations[3].(*int)) = s.values[3].(int)
	*(destinations[4].(*int64)) = s.values[4].(int64)
	*(destinations[5].(*int64)) = s.values[5].(int64)
	*(destinations[6].(*string)) = s.values[6].(string)
	*(destinations[7].(*string)) = s.values[7].(string)
	*(destinations[8].(*sql.NullTime)) = sql.NullTime{Time: s.values[8].(time.Time), Valid: true}
	return nil
}

func TestScanOrderRestoresDomainModel(t *testing.T) {
	createdAt := time.Date(2026, 9, 27, 8, 0, 0, 0, time.UTC)
	got, err := scanOrder(scannerStub{values: []any{
		"order-1", "customer-1", "product-1", 2, int64(12900), int64(25800),
		"confirmed", "key-1", createdAt,
	}})
	if err != nil {
		t.Fatalf("scanOrder() error = %v", err)
	}
	if got.ID() != "order-1" || got.Status() != order.StatusConfirmed || got.TotalCents() != 25800 {
		t.Fatalf("scanOrder() = %+v", got)
	}
}

func TestClassifyInsertOrderError(t *testing.T) {
	for _, tt := range []struct {
		name       string
		constraint string
		want       error
	}{
		{name: "idempotency key", constraint: idempotencyKeyConstraint, want: orderapp.ErrIdempotencyConflict},
		{name: "order ID", constraint: orderPrimaryKeyConstraint, want: ErrDuplicateOrderID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyInsertOrderError(&pgconn.PgError{Code: "23505", ConstraintName: tt.constraint})
			if !errors.Is(err, tt.want) {
				t.Fatalf("classifyInsertOrderError() error = %v, want %v", err, tt.want)
			}
		})
	}
}
