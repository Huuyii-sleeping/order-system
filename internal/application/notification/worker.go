package notification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"example.com/order-system/internal/domain/notification"
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

type Provider interface {
	Send(context.Context, SendRequest) (SendResult, error)
}

type SendRequest struct {
	NotificationID string
	PaymentID      string
	OrderID        string
	RecipientID    string
	EventType      string
	AmountCents    int64
	IdempotencyKey string
}

type SendStatus string

const (
	SendSucceeded SendStatus = "succeeded"
	SendRejected  SendStatus = "rejected"
)

type SendResult struct {
	Status            SendStatus
	ProviderReference string
	Reason            string
}

type TaskOutcome struct {
	Notification notification.Notification
	Reason       string
}

type WorkerConfig struct {
	PollInterval    time.Duration
	BatchSize       int
	MaxConcurrent   int
	LeaseDuration   time.Duration
	RetryBase       time.Duration
	RetryMax        time.Duration
	ProviderTimeout time.Duration
}

type Metrics interface {
	RecordNotificationTask(outcome string)
	RecordNotificationRetry(reason string)
}

type Worker struct {
	store    TaskStore
	provider Provider
	logger   *slog.Logger
	config   WorkerConfig
	now      func() time.Time
	newID    func() (string, error)
	metrics  Metrics
}

const (
	defaultPollInterval    = time.Second
	defaultBatchSize       = 10
	defaultMaxConcurrent   = 4
	defaultLeaseDuration   = 30 * time.Second
	defaultRetryBase       = time.Second
	defaultRetryMax        = time.Minute
	defaultProviderTimeout = 2 * time.Second
)

func NewWorker(store TaskStore, provider Provider, logger *slog.Logger, config WorkerConfig, metrics Metrics) *Worker {
	if config.PollInterval <= 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = defaultBatchSize
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaultMaxConcurrent
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = defaultLeaseDuration
	}
	if config.RetryBase <= 0 {
		config.RetryBase = defaultRetryBase
	}
	if config.RetryMax < config.RetryBase {
		config.RetryMax = defaultRetryMax
	}
	if config.ProviderTimeout <= 0 {
		config.ProviderTimeout = defaultProviderTimeout
	}
	return &Worker{store: store, provider: provider, logger: logger, config: config, now: time.Now, newID: randomLeaseID, metrics: metrics}
}

func (w *Worker) Run(ctx context.Context) { w.RunGracefully(ctx, ctx) }

func (w *Worker) RunGracefully(stopCtx, workCtx context.Context) {
	if w == nil || w.store == nil || w.provider == nil {
		return
	}
	for {
		if stopCtx.Err() != nil || workCtx.Err() != nil {
			return
		}
		processed, err := w.RunOnce(workCtx)
		if err != nil && !errors.Is(err, context.Canceled) && w.logger != nil {
			w.logger.ErrorContext(workCtx, "notification task batch failed", "error", err)
		}
		if stopCtx.Err() != nil || workCtx.Err() != nil {
			return
		}
		if processed == w.config.BatchSize {
			continue
		}
		timer := time.NewTimer(w.config.PollInterval)
		select {
		case <-stopCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-workCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	leaseID, err := w.newID()
	if err != nil {
		return 0, fmt.Errorf("generate notification task lease ID: %w", err)
	}
	now := w.now().UTC()
	tasks, err := w.store.ClaimTasks(ctx, leaseID, w.config.BatchSize, now, now.Add(w.config.LeaseDuration))
	if err != nil {
		return 0, fmt.Errorf("claim notification tasks: %w", err)
	}
	workerCount := min(len(tasks), w.config.MaxConcurrent)
	taskQueue := make(chan Task)
	errorQueue := make(chan error, len(tasks))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for task := range taskQueue {
				if err := w.processTask(ctx, task); err != nil {
					errorQueue <- err
				}
			}
		}()
	}
enqueue:
	for _, task := range tasks {
		select {
		case taskQueue <- task:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(taskQueue)
	workers.Wait()
	close(errorQueue)
	var batchError error
	for taskError := range errorQueue {
		batchError = errors.Join(batchError, taskError)
	}
	return len(tasks), batchError
}

func (w *Worker) processTask(ctx context.Context, task Task) error {
	taskContext := telemetry.Restore(ctx, task.TraceContext)
	taskContext, span := telemetry.Tracer("order-system/notification").Start(taskContext, "notification provider send", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(attribute.String("messaging.system", "notification_task"), attribute.String("messaging.message.id", task.ID), attribute.Int("messaging.message.delivery_attempt", task.Attempts)))
	defer span.End()
	request := SendRequest{NotificationID: task.Notification.ID(), PaymentID: task.Notification.PaymentID(), OrderID: task.Notification.OrderID(), RecipientID: task.Notification.RecipientID(), EventType: task.Notification.EventType(), AmountCents: task.Notification.AmountCents(), IdempotencyKey: task.IdempotencyKey}
	callContext, cancel := context.WithTimeout(taskContext, w.config.ProviderTimeout)
	result, providerErr := w.provider.Send(callContext, request)
	callErr := callContext.Err()
	cancel()
	at := w.now().UTC()
	if providerErr != nil || callErr != nil {
		if providerErr == nil {
			providerErr = callErr
		}
		span.RecordError(providerErr)
		span.SetStatus(codes.Error, providerErr.Error())
		reason := truncateReason(providerErr.Error(), 1000)
		unknown, stateErr := task.Notification.MarkUnknown(reason, at)
		if stateErr != nil {
			return stateErr
		}
		if err := w.store.RetryTask(taskContext, task.ID, task.LeaseID, TaskOutcome{Notification: unknown, Reason: reason}, at.Add(w.retryDelay(task.Attempts))); err != nil {
			return fmt.Errorf("retry notification task %q: %w", task.ID, err)
		}
		if w.metrics != nil {
			w.metrics.RecordNotificationTask("unknown")
			w.metrics.RecordNotificationRetry("provider_error")
		}
		return nil
	}
	var outcome TaskOutcome
	switch result.Status {
	case SendSucceeded:
		updated, stateErr := task.Notification.MarkSent(result.ProviderReference, at)
		if stateErr != nil {
			return stateErr
		}
		outcome = TaskOutcome{Notification: updated}
		if w.metrics != nil {
			w.metrics.RecordNotificationTask("sent")
		}
	case SendRejected:
		reason := truncateReason(result.Reason, 1000)
		updated, stateErr := task.Notification.MarkFailed(reason, at)
		if stateErr != nil {
			return stateErr
		}
		outcome = TaskOutcome{Notification: updated, Reason: reason}
		if w.metrics != nil {
			w.metrics.RecordNotificationTask("rejected")
		}
	default:
		reason := fmt.Sprintf("notification provider returned invalid status %q", result.Status)
		unknown, stateErr := task.Notification.MarkUnknown(reason, at)
		if stateErr != nil {
			return stateErr
		}
		if err := w.store.RetryTask(taskContext, task.ID, task.LeaseID, TaskOutcome{Notification: unknown, Reason: reason}, at.Add(w.retryDelay(task.Attempts))); err != nil {
			return err
		}
		if w.metrics != nil {
			w.metrics.RecordNotificationTask("unknown")
			w.metrics.RecordNotificationRetry("invalid_response")
		}
		return nil
	}
	if err := w.store.CompleteTask(taskContext, task.ID, task.LeaseID, outcome, at); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("complete notification task %q: %w", task.ID, err)
	}
	return nil
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

func randomLeaseID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func truncateReason(value string, limit int) string {
	if value == "" {
		value = "unknown notification provider error"
	}
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
