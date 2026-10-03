package paymentapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"example.com/order-system/internal/application/event"
	"example.com/order-system/internal/application/outbox"
	"example.com/order-system/internal/domain/payment"
	"example.com/order-system/internal/platform/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TaskStore interface {
	ClaimTasks(context.Context, string, int, time.Time, time.Time) ([]Task, error)
	CompleteTask(context.Context, string, string, TaskOutcome, time.Time) error
	RetryTask(context.Context, string, string, TaskOutcome, time.Time) error
}

// Provider 是第三方支付的应用端口。真实 HTTP SDK、测试桩和本地模拟器
// 都通过这个接口接入，应用层不依赖某家支付公司的类型。
type Provider interface {
	Charge(context.Context, ChargeRequest) (ChargeResult, error)
}

type ChargeRequest struct {
	PaymentID      string
	OrderID        string
	AmountCents    int64
	IdempotencyKey string
}

type ChargeStatus string

const (
	ChargeSucceeded ChargeStatus = "succeeded"
	ChargeDeclined  ChargeStatus = "declined"
)

type ChargeResult struct {
	Status            ChargeStatus
	ProviderReference string
	Reason            string
}

type TaskOutcome struct {
	Payment       payment.Payment
	Reason        string
	ResultMessage outbox.Message
}

type WorkerConfig struct {
	PollInterval  time.Duration
	BatchSize     int
	LeaseDuration time.Duration
	RetryBase     time.Duration
	RetryMax      time.Duration
}

type Worker struct {
	store    TaskStore
	provider Provider
	logger   *slog.Logger
	config   WorkerConfig
	now      func() time.Time
	newID    func() (string, error)
}

const (
	defaultPaymentPollInterval  = time.Second
	defaultPaymentBatchSize     = 10
	defaultPaymentLeaseDuration = 30 * time.Second
	defaultPaymentRetryBase     = time.Second
	defaultPaymentRetryMax      = time.Minute
)

func NewWorker(store TaskStore, provider Provider, logger *slog.Logger, config WorkerConfig) *Worker {
	if config.PollInterval <= 0 {
		config.PollInterval = defaultPaymentPollInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = defaultPaymentBatchSize
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaultPaymentLeaseDuration
	}
	if config.RetryBase <= 0 {
		config.RetryBase = defaultPaymentRetryBase
	}
	if config.RetryMax < config.RetryBase {
		config.RetryMax = defaultPaymentRetryMax
	}
	return &Worker{store: store, provider: provider, logger: logger, config: config, now: time.Now, newID: randomTaskLeaseID}
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.store == nil || w.provider == nil {
		return
	}
	for {
		processed, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) && w.logger != nil {
			w.logger.ErrorContext(ctx, "payment task batch failed", "error", err)
		}
		if ctx.Err() != nil {
			return
		}
		if processed == w.config.BatchSize {
			continue
		}
		timer := time.NewTimer(w.config.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

// RunOnce 先用短事务认领任务，再在事务外调用 Provider。网络调用结束后，
// 只有仍持有相同 LeaseID 的 Worker 才能提交最终结果。
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	leaseID, err := w.newID()
	if err != nil {
		return 0, fmt.Errorf("generate payment task lease ID: %w", err)
	}
	now := w.now().UTC()
	tasks, err := w.store.ClaimTasks(ctx, leaseID, w.config.BatchSize, now, now.Add(w.config.LeaseDuration))
	if err != nil {
		return 0, fmt.Errorf("claim payment tasks: %w", err)
	}

	var batchError error
	for _, task := range tasks {
		if err := w.processTask(ctx, task); err != nil {
			batchError = errors.Join(batchError, err)
		}
	}
	return len(tasks), batchError
}

func (w *Worker) processTask(ctx context.Context, task Task) error {
	taskContext := telemetry.Restore(ctx, task.TraceContext)
	taskContext, span := telemetry.Tracer("order-system/payment").Start(
		taskContext, "payment provider charge",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "payment_task"),
			attribute.String("messaging.message.id", task.ID),
			attribute.Int("messaging.message.delivery_attempt", task.Attempts),
		),
	)
	defer span.End()

	// IdempotencyKey 在任务创建时固定，所有重试都必须复用它。
	request := ChargeRequest{
		PaymentID: task.Payment.ID(), OrderID: task.Payment.OrderID(),
		AmountCents: task.Payment.AmountCents(), IdempotencyKey: task.IdempotencyKey,
	}
	result, providerErr := w.provider.Charge(taskContext, request)
	at := w.now().UTC()
	if providerErr != nil {
		span.RecordError(providerErr)
		span.SetStatus(codes.Error, providerErr.Error())
		// 网络错误不能证明扣款失败：请求可能已被第三方执行，只是响应丢失。
		// 因此进入 unknown 并使用相同幂等键重试，而不是直接标记 failed。
		reason := truncateReason(providerErr.Error(), 1000)
		unknown, stateErr := task.Payment.MarkUnknown(reason, at)
		if stateErr != nil {
			span.RecordError(stateErr)
			return stateErr
		}
		outcome := TaskOutcome{Payment: unknown, Reason: reason}
		if err := w.store.RetryTask(taskContext, task.ID, task.LeaseID, outcome, at.Add(w.retryDelay(task.Attempts))); err != nil {
			span.RecordError(err)
			return fmt.Errorf("retry payment task %q: %w", task.ID, err)
		}
		return nil
	}

	var outcome TaskOutcome
	switch result.Status {
	case ChargeSucceeded:
		updated, stateErr := task.Payment.Succeed(result.ProviderReference, at)
		if stateErr != nil {
			span.RecordError(stateErr)
			span.SetStatus(codes.Error, stateErr.Error())
			return stateErr
		}
		outcome = TaskOutcome{Payment: updated}
	case ChargeDeclined:
		// 只有第三方明确拒绝时，才进入不可自动重试的 failed 状态。
		reason := truncateReason(result.Reason, 1000)
		updated, stateErr := task.Payment.Fail(reason, at)
		if stateErr != nil {
			span.RecordError(stateErr)
			span.SetStatus(codes.Error, stateErr.Error())
			return stateErr
		}
		outcome = TaskOutcome{Payment: updated, Reason: reason}
	default:
		reason := fmt.Sprintf("payment provider returned invalid status %q", result.Status)
		unknown, stateErr := task.Payment.MarkUnknown(reason, at)
		if stateErr != nil {
			span.RecordError(stateErr)
			span.SetStatus(codes.Error, stateErr.Error())
			return stateErr
		}
		if err := w.store.RetryTask(
			taskContext, task.ID, task.LeaseID, TaskOutcome{Payment: unknown, Reason: reason},
			at.Add(w.retryDelay(task.Attempts)),
		); err != nil {
			span.RecordError(err)
			return fmt.Errorf("retry payment task %q: %w", task.ID, err)
		}
		span.SetStatus(codes.Error, reason)
		return nil
	}
	resultMessage, err := w.newPaymentResultMessage(taskContext, outcome.Payment)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("create payment result event for task %q: %w", task.ID, err)
	}
	outcome.ResultMessage = resultMessage
	if err := w.store.CompleteTask(taskContext, task.ID, task.LeaseID, outcome, at); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("complete payment task %q: %w", task.ID, err)
	}
	return nil
}

func (w *Worker) newPaymentResultMessage(ctx context.Context, completed payment.Payment) (outbox.Message, error) {
	messageID, err := w.newID()
	if err != nil {
		return outbox.Message{}, fmt.Errorf("generate payment result event ID: %w", err)
	}

	var topic string
	var payload []byte
	switch completed.Status() {
	case payment.StatusSucceeded:
		topic = event.PaymentSucceededV1Topic
		payload, err = json.Marshal(event.PaymentSucceededV1{
			PaymentID: completed.ID(), OrderID: completed.OrderID(),
			CustomerID: completed.CustomerID(), AmountCents: completed.AmountCents(),
			OccurredAt: completed.UpdatedAt(),
		})
	case payment.StatusFailed:
		topic = event.PaymentFailedV1Topic
		payload, err = json.Marshal(event.PaymentFailedV1{
			PaymentID: completed.ID(), OrderID: completed.OrderID(),
			CustomerID: completed.CustomerID(), AmountCents: completed.AmountCents(),
			OccurredAt: completed.UpdatedAt(),
		})
	default:
		return outbox.Message{}, ErrInvalidTask
	}
	if err != nil {
		return outbox.Message{}, fmt.Errorf("encode payment result event: %w", err)
	}
	message, err := outbox.NewMessage(
		messageID, topic, completed.OrderID(), payload, completed.UpdatedAt(),
	)
	if err != nil {
		return outbox.Message{}, fmt.Errorf("create payment result event: %w", err)
	}
	message.TraceContext = telemetry.Capture(ctx)
	return message, nil
}

func (w *Worker) retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := w.config.RetryBase
	for current := 1; current < attempt && delay < w.config.RetryMax; current++ {
		if delay > w.config.RetryMax/2 {
			return w.config.RetryMax
		}
		delay *= 2
	}
	if delay > w.config.RetryMax {
		return w.config.RetryMax
	}
	return delay
}

func randomTaskLeaseID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func truncateReason(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "unknown provider error"
	}
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
