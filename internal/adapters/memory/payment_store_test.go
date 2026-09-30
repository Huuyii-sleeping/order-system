package memory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/memory"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/domain/payment"
)

func TestPaymentStoreRollsBackInboxPaymentAndTask(t *testing.T) {
	store := memory.NewPaymentStore()
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	created, _ := payment.New("payment-1", "order-1", "customer-1", 12900, now)
	task, _ := paymentapp.NewTask("task-1", created)
	wantErr := errors.New("stop transaction")

	err := store.Transact(context.Background(), func(tx paymentapp.Transaction) error {
		begun, err := tx.TryBeginMessage("message-1", "orders.created.v1", strings.Repeat("a", 64), now)
		if err != nil || !begun {
			return err
		}
		if err := tx.SavePayment(created); err != nil {
			return err
		}
		if err := tx.SaveTask(task); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Transact() error = %v", err)
	}
	if _, err := store.GetPaymentByOrderID(context.Background(), "order-1"); !errors.Is(err, payment.ErrPaymentNotFound) {
		t.Fatalf("rolled-back payment error = %v", err)
	}
	err = store.Transact(context.Background(), func(tx paymentapp.Transaction) error {
		begun, err := tx.TryBeginMessage("message-1", "orders.created.v1", strings.Repeat("a", 64), now)
		if err != nil || !begun {
			t.Fatalf("rolled-back inbox begun = %v, err = %v", begun, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("second Transact() error = %v", err)
	}
}

func TestPaymentStoreLeaseExpiresAndRejectsStaleWorker(t *testing.T) {
	store := memory.NewPaymentStore()
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	created, _ := payment.New("payment-1", "order-1", "customer-1", 12900, now)
	task, _ := paymentapp.NewTask("task-1", created)
	if err := store.Transact(context.Background(), func(tx paymentapp.Transaction) error {
		if err := tx.SavePayment(created); err != nil {
			return err
		}
		return tx.SaveTask(task)
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	first, err := store.ClaimTasks(context.Background(), "lease-1", 1, now, now.Add(time.Minute))
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	second, err := store.ClaimTasks(context.Background(), "lease-2", 1, now, now.Add(time.Minute))
	if err != nil || len(second) != 0 {
		t.Fatalf("concurrent claim = %+v, %v", second, err)
	}
	reclaimed, err := store.ClaimTasks(context.Background(), "lease-3", 1, now.Add(time.Minute), now.Add(2*time.Minute))
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("expired claim = %+v, %v", reclaimed, err)
	}
	succeeded, _ := reclaimed[0].Payment.Succeed("provider-1", now.Add(time.Minute))
	outcome := paymentapp.TaskOutcome{Payment: succeeded}
	if err := store.CompleteTask(context.Background(), task.ID, "lease-1", outcome, now); !errors.Is(err, paymentapp.ErrTaskLeaseLost) {
		t.Fatalf("stale completion error = %v", err)
	}
	if err := store.CompleteTask(context.Background(), task.ID, "lease-3", outcome, now); err != nil {
		t.Fatalf("current completion error = %v", err)
	}
}

func TestPaymentStoreScopesPaymentQueryToCustomer(t *testing.T) {
	store := memory.NewPaymentStore()
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	created, err := payment.New("payment-query", "order-query", "customer-1", 12900, now)
	if err != nil {
		t.Fatalf("payment.New() error = %v", err)
	}
	if err := store.Transact(context.Background(), func(tx paymentapp.Transaction) error {
		return tx.SavePayment(created)
	}); err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	got, err := store.GetPaymentByOrderIDAndCustomerID(
		context.Background(), "order-query", "customer-1",
	)
	if err != nil || got.ID() != created.ID() {
		t.Fatalf("owner query = %+v, %v", got, err)
	}
	if _, err := store.GetPaymentByOrderIDAndCustomerID(
		context.Background(), "order-query", "customer-2",
	); !errors.Is(err, payment.ErrPaymentNotFound) {
		t.Fatalf("cross-customer query error = %v, want payment not found", err)
	}
}
