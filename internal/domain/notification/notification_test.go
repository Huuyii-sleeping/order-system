package notification_test

import (
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/domain/notification"
)

func TestProviderConfirmationStateMachine(t *testing.T) {
	createdAt := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	created, err := notification.New("notification-1", "payment-1", "order-1", "customer-1", "customer-1", "payment.succeeded.v1", 12900, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	sent, changed, err := created.ConfirmSent("provider-1", createdAt.Add(time.Second))
	if err != nil || !changed || sent.Status() != notification.StatusSent {
		t.Fatalf("ConfirmSent() = %+v, %v, %v", sent, changed, err)
	}
	duplicate, changed, err := sent.ConfirmSent("provider-1", createdAt.Add(2*time.Second))
	if err != nil || changed || duplicate.UpdatedAt() != sent.UpdatedAt() {
		t.Fatalf("duplicate ConfirmSent() = %+v, %v, %v", duplicate, changed, err)
	}
	if _, _, err := sent.ConfirmFailed("rejected", createdAt.Add(2*time.Second)); !errors.Is(err, notification.ErrInvalidNotificationState) {
		t.Fatalf("opposite terminal transition error = %v", err)
	}
}
