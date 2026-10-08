package event

import "time"

const NotificationRequestedV1Topic = "notifications.requested.v1"

const (
	NotificationPaymentSucceededV1 = "payment_succeeded"
	NotificationPaymentFailedV1    = "payment_failed"
)

// NotificationRequestedV1 是订单状态已经落库后发出的通知请求。
// recipient_id 是产生事件时保存的目标快照，通知模块不需要反向查询订单或用户。
// 如果未来需要真实邮箱，应该在事件生产边界完成授权后的地址解析与脱敏，而不是
// 让通知模块直接依赖订单数据库。
type NotificationRequestedV1 struct {
	NotificationID string    `json:"notification_id"`
	PaymentID      string    `json:"payment_id"`
	OrderID        string    `json:"order_id"`
	CustomerID     string    `json:"customer_id"`
	RecipientID    string    `json:"recipient_id"`
	EventType      string    `json:"event_type"`
	AmountCents    int64     `json:"amount_cents"`
	OccurredAt     time.Time `json:"occurred_at"`
}
