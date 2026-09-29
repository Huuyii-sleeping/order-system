package payment_test

import (
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/domain/payment"
)

func TestPaymentStateMachine(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	created, err := payment.New("payment-1", "order-1", "customer-1", 12900, now)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	processing, err := created.StartProcessing(now.Add(time.Second))
	if err != nil || processing.Status() != payment.StatusProcessing {
		t.Fatalf("StartProcessing() = %+v, %v", processing, err)
	}
	succeeded, err := processing.Succeed("provider-1", now.Add(2*time.Second))
	if err != nil || succeeded.Status() != payment.StatusSucceeded ||
		succeeded.ProviderReference() != "provider-1" {
		t.Fatalf("Succeed() = %+v, %v", succeeded, err)
	}
	if _, err := succeeded.StartProcessing(now.Add(3 * time.Second)); !errors.Is(err, payment.ErrInvalidPaymentState) {
		t.Fatalf("restart succeeded payment error = %v", err)
	}
}

func TestUnknownPaymentCanBeRetried(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	created, _ := payment.New("payment-1", "order-1", "customer-1", 12900, now)
	processing, _ := created.StartProcessing(now)
	unknown, err := processing.MarkUnknown("provider timeout", now.Add(time.Second))
	if err != nil || unknown.Status() != payment.StatusUnknown {
		t.Fatalf("MarkUnknown() = %+v, %v", unknown, err)
	}
	retrying, err := unknown.StartProcessing(now.Add(2 * time.Second))
	if err != nil || retrying.Status() != payment.StatusProcessing || retrying.FailureReason() != "" {
		t.Fatalf("retry StartProcessing() = %+v, %v", retrying, err)
	}
}

func TestPaymentRejectsInvalidCreationAndTransition(t *testing.T) {
	now := time.Now()
	if _, err := payment.New("", "order-1", "customer-1", 100, now); !errors.Is(err, payment.ErrInvalidPayment) {
		t.Fatalf("invalid New() error = %v", err)
	}
	created, _ := payment.New("payment-1", "order-1", "customer-1", 100, now)
	if _, err := created.Succeed("provider-1", now); !errors.Is(err, payment.ErrInvalidPaymentState) {
		t.Fatalf("pending Succeed() error = %v", err)
	}
}
