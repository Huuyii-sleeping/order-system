package notification

import (
	"context"
	"fmt"
	"strings"

	domainnotification "example.com/order-system/internal/domain/notification"
)

type QueryStore interface {
	GetNotificationByOrderIDAndCustomerID(context.Context, string, string) (domainnotification.Notification, error)
}

type QueryService struct{ store QueryStore }

func NewQueryService(store QueryStore) *QueryService { return &QueryService{store: store} }

func (s *QueryService) GetNotification(ctx context.Context, customerID, orderID string) (domainnotification.Notification, error) {
	customerID, orderID = strings.TrimSpace(customerID), strings.TrimSpace(orderID)
	if customerID == "" || orderID == "" {
		return domainnotification.Notification{}, domainnotification.ErrNotificationNotFound
	}
	got, err := s.store.GetNotificationByOrderIDAndCustomerID(ctx, orderID, customerID)
	if err != nil {
		return domainnotification.Notification{}, fmt.Errorf("get notification for order %q: %w", orderID, err)
	}
	return got, nil
}
