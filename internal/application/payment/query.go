package paymentapp

import (
	"context"
	"fmt"
	"strings"

	"example.com/order-system/internal/domain/payment"
)

// QueryStore 只暴露支付查询需要的最小存储能力。
// customerID 是查询条件的一部分，避免先读取别人的支付再在应用层过滤。
type QueryStore interface {
	GetPaymentByOrderIDAndCustomerID(context.Context, string, string) (payment.Payment, error)
}

// QueryService 提供面向 HTTP 等入口的支付读取用例。
// 它和 Consumer、Worker 共享同一份存储，但不承担消息消费或任务执行职责。
type QueryService struct {
	store QueryStore
}

func NewQueryService(store QueryStore) *QueryService {
	return &QueryService{store: store}
}

func (s *QueryService) GetPayment(
	ctx context.Context, customerID, orderID string,
) (payment.Payment, error) {
	customerID = strings.TrimSpace(customerID)
	orderID = strings.TrimSpace(orderID)
	if customerID == "" || orderID == "" {
		return payment.Payment{}, payment.ErrPaymentNotFound
	}

	got, err := s.store.GetPaymentByOrderIDAndCustomerID(ctx, orderID, customerID)
	if err != nil {
		return payment.Payment{}, fmt.Errorf("get payment for order %q: %w", orderID, err)
	}
	return got, nil
}
