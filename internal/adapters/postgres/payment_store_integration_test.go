package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/order-system/internal/adapters/messaging"
	"example.com/order-system/internal/adapters/postgres"
	"example.com/order-system/internal/application/event"
	orderapp "example.com/order-system/internal/application/order"
	"example.com/order-system/internal/application/outbox"
	paymentapp "example.com/order-system/internal/application/payment"
	"example.com/order-system/internal/domain/order"
	"example.com/order-system/internal/domain/payment"
)

type paymentProviderStub struct {
	mu           sync.Mutex
	failuresLeft int
	requests     []paymentapp.ChargeRequest
}

func (p *paymentProviderStub) Charge(
	_ context.Context, request paymentapp.ChargeRequest,
) (paymentapp.ChargeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, request)
	if p.failuresLeft > 0 {
		p.failuresLeft--
		return paymentapp.ChargeResult{}, errors.New("simulated timeout")
	}
	return paymentapp.ChargeResult{
		Status: paymentapp.ChargeSucceeded, ProviderReference: "provider-payment-1",
	}, nil
}

func TestIntegrationInboxPaymentAndProviderRecovery(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "payment-customer")

	orderIDs := &sequenceIDGenerator{prefix: "payment-source"}
	orderService := newPostgresOrderService(db, orderIDs)
	createdOrder, err := orderService.CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "payment-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "payment-order-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	paymentStore := postgres.NewPaymentStore(db)
	orderStore := postgres.NewOrderStore(db)
	paymentIDs := &sequenceIDGenerator{prefix: "payment"}
	consumer := paymentapp.NewConsumer(paymentStore, paymentIDs, fixedClock{})
	resultConsumer := orderapp.NewPaymentResultConsumer(orderStore, fixedClock{})
	publisher := messaging.NewDirectPublisher(nil, map[string]messaging.MessageHandler{
		orderapp.OrderCreatedTopic:    consumer,
		event.PaymentSucceededV1Topic: resultConsumer,
		event.PaymentFailedV1Topic:    resultConsumer,
	})
	var messageID string
	var payload []byte
	if err := db.QueryRowContext(ctx, `
		SELECT id, payload FROM outbox_messages WHERE message_key = $1
	`, createdOrder.Order.ID()).Scan(&messageID, &payload); err != nil {
		t.Fatalf("query source event: %v", err)
	}
	redelivery, err := outbox.NewMessage(
		messageID, orderapp.OrderCreatedTopic, createdOrder.Order.ID(), payload, fixedClock{}.Now(),
	)
	if err != nil {
		t.Fatalf("restore redelivery: %v", err)
	}

	// 模拟 Broker 并发投递同一条事件。Inbox 主键使两个事务最终只产生一份业务结果。
	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			errorsFound <- publisher.Publish(ctx, redelivery)
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Errorf("duplicate Publish() error = %v", err)
		}
	}

	var inboxCount, paymentCount, taskCount int
	if err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM inbox_messages WHERE message_id = $1),
			(SELECT count(*) FROM payments WHERE order_id = $2),
			(SELECT count(*) FROM payment_tasks)
	`, messageID, createdOrder.Order.ID()).Scan(&inboxCount, &paymentCount, &taskCount); err != nil {
		t.Fatalf("count consumer records: %v", err)
	}
	if inboxCount != 1 || paymentCount != 1 || taskCount != 1 {
		t.Fatalf("inbox/payment/task = %d/%d/%d; want 1/1/1", inboxCount, paymentCount, taskCount)
	}

	// Outbox 中的原始事件随后再次投递，仍会命中同一条 Inbox 记录。
	outboxStore := postgres.NewOutboxStore(db)
	outboxWorker := outbox.NewWorker(outboxStore, publisher, nil, outbox.WorkerConfig{
		BatchSize: 10, LeaseDuration: time.Second,
	})
	if processed, err := outboxWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("outbox RunOnce() = %d, %v; want 1, nil", processed, err)
	}

	provider := &paymentProviderStub{failuresLeft: 1}
	paymentWorker := paymentapp.NewWorker(paymentStore, provider, nil, paymentapp.WorkerConfig{
		BatchSize: 10, LeaseDuration: time.Second,
		RetryBase: 10 * time.Millisecond, RetryMax: time.Second,
	})
	if processed, err := paymentWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("first payment RunOnce() = %d, %v", processed, err)
	}
	stored, err := paymentStore.GetPaymentByOrderID(ctx, createdOrder.Order.ID())
	if err != nil || stored.Status() != payment.StatusUnknown {
		t.Fatalf("payment after timeout = %+v, %v", stored, err)
	}
	time.Sleep(15 * time.Millisecond)
	if processed, err := paymentWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("second payment RunOnce() = %d, %v", processed, err)
	}
	stored, err = paymentStore.GetPaymentByOrderID(ctx, createdOrder.Order.ID())
	if err != nil || stored.Status() != payment.StatusSucceeded {
		t.Fatalf("payment after recovery = %+v, %v", stored, err)
	}
	if len(provider.requests) != 2 ||
		provider.requests[0].IdempotencyKey != provider.requests[1].IdempotencyKey {
		t.Fatalf("provider requests = %+v; want stable idempotency key", provider.requests)
	}
	if processed, err := outboxWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("payment result outbox RunOnce() = %d, %v; want 1, nil", processed, err)
	}
	confirmed, err := orderStore.GetOrderByIDAndCustomerID(
		ctx, createdOrder.Order.ID(), "payment-customer",
	)
	if err != nil || confirmed.Status() != order.StatusConfirmed {
		t.Fatalf("order after payment = %+v, %v; want confirmed", confirmed, err)
	}
}

type declinedPaymentProvider struct{}

func (declinedPaymentProvider) Charge(
	context.Context, paymentapp.ChargeRequest,
) (paymentapp.ChargeResult, error) {
	return paymentapp.ChargeResult{
		Status: paymentapp.ChargeDeclined, Reason: "fixture decline",
	}, nil
}

func TestIntegrationFailedPaymentCancelsOrderAndReleasesStockOnce(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "failed-payment-customer")

	orderService := newPostgresOrderService(
		db, &sequenceIDGenerator{prefix: "failed-payment-source"},
	)
	created, err := orderService.CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "failed-payment-customer", ProductID: "product-1",
		Quantity: 2, IdempotencyKey: "failed-payment-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	paymentStore := postgres.NewPaymentStore(db)
	orderStore := postgres.NewOrderStore(db)
	paymentConsumer := paymentapp.NewConsumer(
		paymentStore, &sequenceIDGenerator{prefix: "failed-payment"}, fixedClock{},
	)
	resultConsumer := orderapp.NewPaymentResultConsumer(orderStore, fixedClock{})
	publisher := messaging.NewDirectPublisher(nil, map[string]messaging.MessageHandler{
		orderapp.OrderCreatedTopic:    paymentConsumer,
		event.PaymentSucceededV1Topic: resultConsumer,
		event.PaymentFailedV1Topic:    resultConsumer,
	})
	outboxWorker := outbox.NewWorker(
		postgres.NewOutboxStore(db), publisher, nil,
		outbox.WorkerConfig{BatchSize: 10, LeaseDuration: time.Second},
	)
	if processed, err := outboxWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("order outbox RunOnce() = %d, %v", processed, err)
	}
	paymentWorker := paymentapp.NewWorker(
		paymentStore, declinedPaymentProvider{}, nil,
		paymentapp.WorkerConfig{BatchSize: 10, LeaseDuration: time.Second},
	)
	if processed, err := paymentWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("payment RunOnce() = %d, %v", processed, err)
	}
	if processed, err := outboxWorker.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("payment result outbox RunOnce() = %d, %v", processed, err)
	}

	cancelled, err := orderStore.GetOrderByIDAndCustomerID(
		ctx, created.Order.ID(), "failed-payment-customer",
	)
	if err != nil || cancelled.Status() != order.StatusCancelled {
		t.Fatalf("order after decline = %+v, %v; want cancelled", cancelled, err)
	}
	if available := queryAvailable(t, db, "product-1"); available != 10 {
		t.Fatalf("stock after compensation = %d, want 10", available)
	}

	var eventID string
	var payload []byte
	var occurredAt time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT id, payload, occurred_at FROM outbox_messages
		WHERE topic = $1 AND message_key = $2
	`, event.PaymentFailedV1Topic, created.Order.ID()).Scan(
		&eventID, &payload, &occurredAt,
	); err != nil {
		t.Fatalf("query failed payment event: %v", err)
	}
	redelivery, err := outbox.NewMessage(
		eventID, event.PaymentFailedV1Topic, created.Order.ID(), payload, occurredAt,
	)
	if err != nil {
		t.Fatalf("restore failed payment event: %v", err)
	}
	if err := publisher.Publish(ctx, redelivery); err != nil {
		t.Fatalf("duplicate failed event: %v", err)
	}
	if available := queryAvailable(t, db, "product-1"); available != 10 {
		t.Fatalf("stock after duplicate event = %d, want 10", available)
	}
}

func TestIntegrationPaymentConsumerTransactionRollsBack(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "rollback-payment-customer")
	orderService := newPostgresOrderService(db, &sequenceIDGenerator{prefix: "rollback-payment"})
	createdOrder, err := orderService.CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "rollback-payment-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "rollback-payment-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	store := postgres.NewPaymentStore(db)
	now := fixedClock{}.Now()
	createdPayment, _ := payment.New(
		"rollback-payment", createdOrder.Order.ID(), "rollback-payment-customer", 12900, now,
	)
	task, _ := paymentapp.NewTask("rollback-task", createdPayment)
	wantErr := errors.New("stop consumer transaction")
	err = store.Transact(ctx, func(tx paymentapp.Transaction) error {
		begun, err := tx.TryBeginMessage("rollback-message", orderapp.OrderCreatedTopic,
			strings.Repeat("a", 64), now)
		if err != nil || !begun {
			return err
		}
		if err := tx.SavePayment(createdPayment); err != nil {
			return err
		}
		if err := tx.SaveTask(task); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Transact() error = %v", err)
	}

	var inboxCount, paymentCount, taskCount int
	if err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT count(*) FROM inbox_messages WHERE message_id = 'rollback-message'),
			(SELECT count(*) FROM payments WHERE id = 'rollback-payment'),
			(SELECT count(*) FROM payment_tasks WHERE id = 'rollback-task')
	`).Scan(&inboxCount, &paymentCount, &taskCount); err != nil {
		t.Fatalf("count rolled-back rows: %v", err)
	}
	if inboxCount != 0 || paymentCount != 0 || taskCount != 0 {
		t.Fatalf("rolled-back inbox/payment/task = %d/%d/%d; want 0/0/0",
			inboxCount, paymentCount, taskCount)
	}
}

func TestIntegrationPaymentCompletionRollsBackWhenOutboxWriteFails(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "atomic-payment-customer")
	createdOrder, err := newPostgresOrderService(
		db, &sequenceIDGenerator{prefix: "atomic-payment-source"},
	).CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "atomic-payment-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "atomic-payment-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	store := postgres.NewPaymentStore(db)
	paymentConsumer := paymentapp.NewConsumer(
		store, &sequenceIDGenerator{prefix: "atomic-payment"}, fixedClock{},
	)
	payload, _ := json.Marshal(event.OrderCreatedV1{
		OrderID: createdOrder.Order.ID(), CustomerID: "atomic-payment-customer",
		ProductID: "product-1", Quantity: 1, TotalCents: 12900,
		OccurredAt: fixedClock{}.Now(),
	})
	message, _ := outbox.NewMessage(
		"atomic-order-event", orderapp.OrderCreatedTopic, createdOrder.Order.ID(),
		payload, fixedClock{}.Now(),
	)
	if _, err := paymentConsumer.Consume(ctx, message); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	now := time.Now().UTC()
	tasks, err := store.ClaimTasks(ctx, "atomic-lease", 1, now, now.Add(time.Minute))
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ClaimTasks() = %+v, %v", tasks, err)
	}
	succeeded, err := tasks[0].Payment.Succeed("provider-atomic", now)
	if err != nil {
		t.Fatalf("Succeed() error = %v", err)
	}
	resultPayload, _ := json.Marshal(event.PaymentSucceededV1{
		PaymentID: succeeded.ID(), OrderID: succeeded.OrderID(),
		CustomerID: succeeded.CustomerID(), AmountCents: succeeded.AmountCents(),
		OccurredAt: succeeded.UpdatedAt(),
	})
	resultMessage, _ := outbox.NewMessage(
		"duplicate-result-id", event.PaymentSucceededV1Topic, succeeded.OrderID(),
		resultPayload, succeeded.UpdatedAt(),
	)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO outbox_messages (id, topic, message_key, payload, occurred_at, next_attempt_at)
		VALUES ($1, 'fixture.topic', 'fixture-key', '{}', $2, $2)
	`, resultMessage.ID, now); err != nil {
		t.Fatalf("seed conflicting outbox message: %v", err)
	}

	err = store.CompleteTask(ctx, tasks[0].ID, tasks[0].LeaseID, paymentapp.TaskOutcome{
		Payment: succeeded, ResultMessage: resultMessage,
	}, now)
	if err == nil {
		t.Fatal("CompleteTask() error = nil, want outbox insert failure")
	}
	stored, err := store.GetPaymentByOrderID(ctx, createdOrder.Order.ID())
	if err != nil || stored.Status() != payment.StatusProcessing {
		t.Fatalf("payment after rollback = %+v, %v; want processing", stored, err)
	}
	var completedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT completed_at FROM payment_tasks WHERE id = $1
	`, tasks[0].ID).Scan(&completedAt); err != nil {
		t.Fatalf("query payment task: %v", err)
	}
	if completedAt.Valid {
		t.Fatalf("payment task completed_at = %s, want NULL after rollback", completedAt.Time)
	}
}

func TestIntegrationConcurrentPaymentTaskClaim(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "claim-payment-customer")
	orderService := newPostgresOrderService(db, &sequenceIDGenerator{prefix: "claim-payment"})
	createdOrder, err := orderService.CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "claim-payment-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "claim-payment-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	payload, _ := json.Marshal(event.OrderCreatedV1{
		OrderID: createdOrder.Order.ID(), CustomerID: "claim-payment-customer",
		ProductID: "product-1", Quantity: 1, TotalCents: 12900, OccurredAt: fixedClock{}.Now(),
	})
	message, _ := outbox.NewMessage("claim-message", orderapp.OrderCreatedTopic, createdOrder.Order.ID(), payload, fixedClock{}.Now())
	store := postgres.NewPaymentStore(db)
	consumer := paymentapp.NewConsumer(store, &sequenceIDGenerator{prefix: "claim-task"}, fixedClock{})
	if _, err := consumer.Consume(ctx, message); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}

	start := make(chan struct{})
	results := make(chan []paymentapp.Task, 2)
	errorsFound := make(chan error, 2)
	var waitGroup sync.WaitGroup
	now := time.Now().UTC()
	for _, leaseID := range []string{"payment-lease-a", "payment-lease-b"} {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			tasks, err := store.ClaimTasks(ctx, leaseID, 1, now, now.Add(time.Minute))
			if err != nil {
				errorsFound <- err
				return
			}
			results <- tasks
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent ClaimTasks() error = %v", err)
	}
	claimed := 0
	for tasks := range results {
		claimed += len(tasks)
	}
	if claimed != 1 {
		t.Fatalf("concurrently claimed payment tasks = %d, want 1", claimed)
	}
}

func TestIntegrationPaymentQueryIsScopedToCustomer(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := openMigratedIdentityTestDB(t, ctx, databaseURL)
	seedIntegrationUsers(t, db, "query-customer", "other-customer")
	orderService := newPostgresOrderService(db, &sequenceIDGenerator{prefix: "query-payment"})
	createdOrder, err := orderService.CreateOrder(ctx, orderapp.CreateOrderCommand{
		CustomerID: "query-customer", ProductID: "product-1",
		Quantity: 1, IdempotencyKey: "query-payment-key",
	})
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}

	store := postgres.NewPaymentStore(db)
	consumer := paymentapp.NewConsumer(
		store, &sequenceIDGenerator{prefix: "query-payment-consumer"}, fixedClock{},
	)
	payload, err := json.Marshal(event.OrderCreatedV1{
		OrderID: createdOrder.Order.ID(), CustomerID: "query-customer",
		ProductID: "product-1", Quantity: 1, TotalCents: 12900, OccurredAt: fixedClock{}.Now(),
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	message, err := outbox.NewMessage(
		"query-payment-message", orderapp.OrderCreatedTopic, createdOrder.Order.ID(),
		payload, fixedClock{}.Now(),
	)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if _, err := consumer.Consume(ctx, message); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}

	got, err := store.GetPaymentByOrderIDAndCustomerID(
		ctx, createdOrder.Order.ID(), "query-customer",
	)
	if err != nil || got.OrderID() != createdOrder.Order.ID() {
		t.Fatalf("owner query = %+v, %v", got, err)
	}
	if _, err := store.GetPaymentByOrderIDAndCustomerID(
		ctx, createdOrder.Order.ID(), "other-customer",
	); !errors.Is(err, payment.ErrPaymentNotFound) {
		t.Fatalf("cross-customer query error = %v, want payment not found", err)
	}
}
