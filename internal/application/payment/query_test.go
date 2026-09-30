package paymentapp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/domain/payment"
)

type paymentQueryStoreStub struct {
	payment    payment.Payment
	err        error
	orderID    string
	customerID string
}

func (s *paymentQueryStoreStub) GetPaymentByOrderIDAndCustomerID(
	_ context.Context, orderID, customerID string,
) (payment.Payment, error) {
	s.orderID = orderID
	s.customerID = customerID
	return s.payment, s.err
}

func TestQueryServiceScopesPaymentToAuthenticatedCustomer(t *testing.T) {
	created, err := payment.New(
		"payment-1", "order-1", "customer-1", 12900,
		time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("payment.New() error = %v", err)
	}
	store := &paymentQueryStoreStub{payment: created}
	service := paymentapp.NewQueryService(store)

	got, err := service.GetPayment(context.Background(), " customer-1 ", " order-1 ")

	if err != nil {
		t.Fatalf("GetPayment() error = %v", err)
	}
	if got.ID() != created.ID() {
		t.Errorf("payment ID = %q, want %q", got.ID(), created.ID())
	}
	if store.customerID != "customer-1" || store.orderID != "order-1" {
		t.Errorf("store query = customer %q, order %q", store.customerID, store.orderID)
	}
}

func TestQueryServicePreservesNotFoundClassification(t *testing.T) {
	service := paymentapp.NewQueryService(&paymentQueryStoreStub{err: payment.ErrPaymentNotFound})

	_, err := service.GetPayment(context.Background(), "customer-1", "order-1")

	if !errors.Is(err, payment.ErrPaymentNotFound) {
		t.Fatalf("GetPayment() error = %v, want payment not found", err)
	}
}

func TestQueryServiceRejectsEmptyIdentityWithoutCallingStore(t *testing.T) {
	store := &paymentQueryStoreStub{}
	service := paymentapp.NewQueryService(store)

	_, err := service.GetPayment(context.Background(), " ", "order-1")

	if !errors.Is(err, payment.ErrPaymentNotFound) {
		t.Fatalf("GetPayment() error = %v, want payment not found", err)
	}
	if store.orderID != "" || store.customerID != "" {
		t.Fatal("invalid query must not call store")
	}
}
