// Package event 定义跨应用模块传递的稳定集成事件契约。
package event

import "time"

const OrderCreatedV1Topic = "orders.created.v1"

// OrderCreatedV1 是订单创建事件的第一版公开结构。字段发生不兼容变化时，
// 应新增 v2 契约，而不是直接改变已经被消费者依赖的语义。
type OrderCreatedV1 struct {
	OrderID    string    `json:"order_id"`
	CustomerID string    `json:"customer_id"`
	ProductID  string    `json:"product_id"`
	Quantity   int       `json:"quantity"`
	TotalCents int64     `json:"total_cents"`
	OccurredAt time.Time `json:"occurred_at"`
}
