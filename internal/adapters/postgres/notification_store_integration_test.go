package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/messaging"
	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/application/event"
	notificationapp "example.com/order-system/internal/application/notification"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	domainnotification "example.com/order-system/internal/domain/notification"
	"example.com/order-system/internal/domain/order"
)

// TestIntegrationNotificationDelivery 验证从支付结果到通知 Provider 的完整
// PostgreSQL 链路：订单事务写通知 Outbox、通知 Inbox 幂等、任务重试、
// 稳定幂等键，以及按 customer_id 隔离查询。
func TestIntegrationNotificationDelivery(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "notification-customer", "other-notification-customer")

	created, err := newPostgresOrderService(
		db, &sequenceIDGenerator{prefix: "notification-order"},
	).CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "notification-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "notification-order-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	orderStore := postgres.NewOrderStore(db)
	paymentStore := postgres.NewPaymentStore(db)
	notificationStore := postgres.NewNotificationStore(db)
	paymentConsumer := paymentapp.NewConsumer(
		paymentStore, &sequenceIDGenerator{prefix: "notification-payment"}, fixedClock{},
	)
	resultConsumer := orderapp.NewPaymentResultConsumer(orderStore, fixedClock{})
	notificationConsumer := notificationapp.NewConsumer(
		notificationStore, &sequenceIDGenerator{prefix: "notification-task"}, fixedClock{},
	)
	publisher := messaging.NewDirectPublisher(nil, map[string]messaging.MessageHandler{
		orderapp.OrderCreatedTopic:         paymentConsumer,
		event.PaymentSucceededV1Topic:      resultConsumer,
		event.PaymentFailedV1Topic:         resultConsumer,
		event.NotificationRequestedV1Topic: notificationConsumer,
	})
	outboxWorker := outbox.NewWorker(
		postgres.NewOutboxStore(db), publisher, nil,
		outbox.WorkerConfig{BatchSize: 10, LeaseDuration: time.Second},
	)

	// 1. 订单 Outbox -> 支付 Inbox/Task。
	if processed, err := outboxWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("order outbox RunOnce() = %d, %v; want 1", processed, err)
	}

	// 2. 支付 Worker 成功后，把支付结果写入支付 Store 的 Outbox。
	paymentWorker := paymentapp.NewWorker(
		paymentStore, successfulPaymentProvider{}, nil,
		paymentapp.WorkerConfig{BatchSize: 10, LeaseDuration: time.Second},
	)
	if processed, err := paymentWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("payment RunOnce() = %d, %v; want 1", processed, err)
	}

	// 3. 支付结果 Outbox -> 订单状态和通知请求在同一个订单事务中提交。
	if processed, err := outboxWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("payment result outbox RunOnce() = %d, %v; want 1", processed, err)
	}
	confirmed, err := orderStore.GetOrderByIDAndCustomerID(
		ctx, created.Order.ID(), "notification-customer",
	)
	if err != nil || confirmed.Status() != order.StatusConfirmed {
		t.Fatalf("order after payment result = %+v, %v; want confirmed", confirmed, err)
	}
	var notificationOutboxCount int
	if err := db.QueryRowContext(ctx, `
		SELECT count(*) FROM outbox_messages
		WHERE topic = $1 AND message_key = $2
	`, event.NotificationRequestedV1Topic, created.Order.ID()).Scan(&notificationOutboxCount); err != nil {
		t.Fatalf("count notification outbox messages: %v", err)
	}
	if notificationOutboxCount != 1 {
		t.Fatalf("notification outbox messages = %d, want 1", notificationOutboxCount)
	}

	// 4. 通知 Outbox -> 通知 Inbox、Notification 和 Task 一起提交。
	if processed, err := outboxWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("notification outbox RunOnce() = %d, %v; want 1", processed, err)
	}
	pending, err := notificationStore.GetNotificationByOrderIDAndCustomerID(
		ctx, created.Order.ID(), "notification-customer",
	)
	if err != nil || pending.Status() != domainnotification.StatusPending {
		t.Fatalf("notification after consume = %+v, %v; want pending", pending, err)
	}

	// 5. 第一次 Provider 已经发送成功，但模拟响应丢失；第二轮先按稳定幂等键
	// 查询对账并收敛为 sent，不会再次调用 Send。
	provider := &notificationProviderStub{loseResponseAfterSend: true}
	notificationWorker := notificationapp.NewWorker(
		notificationStore, provider, nil,
		notificationapp.WorkerConfig{
			BatchSize: 1, LeaseDuration: time.Second,
			RetryBase: 10 * time.Millisecond, RetryMax: time.Second,
			ProviderTimeout: time.Second,
		}, nil,
	)
	if processed, err := notificationWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("first notification RunOnce() = %d, %v", processed, err)
	}
	unknown, err := notificationStore.GetNotificationByOrderIDAndCustomerID(
		ctx, created.Order.ID(), "notification-customer",
	)
	if err != nil || unknown.Status() != domainnotification.StatusUnknown {
		t.Fatalf("notification after provider error = %+v, %v; want unknown", unknown, err)
	}
	time.Sleep(15 * time.Millisecond)
	if processed, err := notificationWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("second notification RunOnce() = %d, %v", processed, err)
	}
	sent, err := notificationStore.GetNotificationByOrderIDAndCustomerID(
		ctx, created.Order.ID(), "notification-customer",
	)
	if err != nil || sent.Status() != domainnotification.StatusSent {
		t.Fatalf("notification after recovery = %+v, %v; want sent", sent, err)
	}
	if len(provider.requests) != 1 || provider.lookups != 1 {
		t.Fatalf("provider sends/lookups = %d/%d; want 1/1", len(provider.requests), provider.lookups)
	}

	// 6. 重复投递同一通知事件只命中 Inbox，不会新增 Notification/Task。
	var eventID string
	var payload []byte
	var occurredAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT id, payload, occurred_at FROM outbox_messages
		WHERE topic = $1 AND message_key = $2
	`, event.NotificationRequestedV1Topic, created.Order.ID()).Scan(&eventID, &payload, &occurredAt); err != nil {
		t.Fatalf("query notification outbox message: %v", err)
	}
	duplicate, err := outbox.NewMessage(
		eventID, event.NotificationRequestedV1Topic, created.Order.ID(), payload, occurredAt,
	)
	if err != nil {
		t.Fatalf("restore duplicate notification message: %v", err)
	}
	if err := publisher.Publish(ctx, duplicate); err != nil {
		t.Fatalf("duplicate notification Publish() error = %v", err)
	}
	var inboxCount, notificationCount, taskCount int
	if err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM inbox_messages WHERE message_id = $1),
			(SELECT count(*) FROM notifications WHERE order_id = $2),
			(SELECT count(*) FROM notification_tasks WHERE notification_id = $3)
	`, eventID, created.Order.ID(), sent.ID()).Scan(&inboxCount, &notificationCount, &taskCount); err != nil {
		t.Fatalf("count duplicate notification records: %v", err)
	}
	if inboxCount != 1 || notificationCount != 1 || taskCount != 1 {
		t.Fatalf("duplicate notification records = %d/%d/%d; want 1/1/1", inboxCount, notificationCount, taskCount)
	}

	if _, err := notificationStore.GetNotificationByOrderIDAndCustomerID(
		ctx, created.Order.ID(), "other-notification-customer",
	); !errors.Is(err, domainnotification.ErrNotificationNotFound) {
		t.Fatalf("cross-customer notification query error = %v; want not found", err)
	}
}

type successfulPaymentProvider struct{}

func (successfulPaymentProvider) Charge(
	context.Context, paymentapp.ChargeRequest,
) (paymentapp.ChargeResult, error) {
	return paymentapp.ChargeResult{
		Status: paymentapp.ChargeSucceeded, ProviderReference: "integration-payment-reference",
	}, nil
}

type notificationProviderStub struct {
	mu                    sync.Mutex
	loseResponseAfterSend bool
	requests              []notificationapp.SendRequest
	results               map[string]notificationapp.SendResult
	lookups               int
}

func (p *notificationProviderStub) Lookup(
	_ context.Context, idempotencyKey string,
) (notificationapp.LookupResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lookups++
	result, found := p.results[idempotencyKey]
	return notificationapp.LookupResult{Found: found, Result: result}, nil
}

func (p *notificationProviderStub) Send(
	_ context.Context, request notificationapp.SendRequest,
) (notificationapp.SendResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	result := notificationapp.SendResult{
		Status: notificationapp.SendSucceeded, ProviderReference: "integration-notification-reference",
	}
	if p.results == nil {
		p.results = make(map[string]notificationapp.SendResult)
	}
	p.results[request.IdempotencyKey] = result
	if p.loseResponseAfterSend {
		p.loseResponseAfterSend = false
		return notificationapp.SendResult{}, errors.New("simulated notification response lost")
	}
	return result, nil
}
