// Package paymentprovider 提供第三方支付接口的本地适配器。
package paymentprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	paymentapp "example.com/order-system/internal/application/payment"
)

var ErrIdempotencyConflict = errors.New("payment provider idempotency key reused with different request")

type recordedCharge struct {
	request paymentapp.ChargeRequest
	result  paymentapp.ChargeResult
}

// Simulated 是开发环境中的确定性支付提供方。它会缓存每个幂等键的第一次结果，
// 因此即使 Worker 因响应丢失而重试，也不会重复产生扣款效果。
type Simulated struct {
	mu      sync.Mutex
	charges map[string]recordedCharge
}

func NewSimulated() *Simulated {
	return &Simulated{charges: make(map[string]recordedCharge)}
}

func (s *Simulated) Charge(
	ctx context.Context, request paymentapp.ChargeRequest,
) (paymentapp.ChargeResult, error) {
	if err := ctx.Err(); err != nil {
		return paymentapp.ChargeResult{}, err
	}
	request.PaymentID = strings.TrimSpace(request.PaymentID)
	request.OrderID = strings.TrimSpace(request.OrderID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.PaymentID == "" || request.OrderID == "" ||
		request.IdempotencyKey == "" || request.AmountCents <= 0 {
		return paymentapp.ChargeResult{}, errors.New("invalid simulated charge request")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.charges[request.IdempotencyKey]; ok {
		if existing.request != request {
			return paymentapp.ChargeResult{}, ErrIdempotencyConflict
		}
		return existing.result, nil
	}
	result := paymentapp.ChargeResult{
		Status:            paymentapp.ChargeSucceeded,
		ProviderReference: fmt.Sprintf("simulated-%s", request.PaymentID),
	}
	s.charges[request.IdempotencyKey] = recordedCharge{request: request, result: result}
	return result, nil
}
