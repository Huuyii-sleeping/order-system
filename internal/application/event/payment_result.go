package event

import "time"

const (
	PaymentSucceededV1Topic = "payments.succeeded.v1"
	PaymentFailedV1Topic    = "payments.failed.v1"
)

// PaymentSucceededV1 是支付模块对外发布的成功事实。
// 它只包含其他模块推进流程所需的稳定字段，不泄露支付渠道内部响应。
type PaymentSucceededV1 struct {
	PaymentID   string    `json:"payment_id"`
	OrderID     string    `json:"order_id"`
	CustomerID  string    `json:"customer_id"`
	AmountCents int64     `json:"amount_cents"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// PaymentFailedV1 表示支付渠道已经明确拒绝，而不是结果仍未知。
// 原始失败原因可能包含渠道细节或敏感数据，因此不进入跨模块事件契约。
type PaymentFailedV1 struct {
	PaymentID   string    `json:"payment_id"`
	OrderID     string    `json:"order_id"`
	CustomerID  string    `json:"customer_id"`
	AmountCents int64     `json:"amount_cents"`
	OccurredAt  time.Time `json:"occurred_at"`
}
