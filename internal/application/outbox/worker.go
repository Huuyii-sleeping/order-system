package outbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"example.com/order-system/internal/platform/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type WorkerConfig struct {
	PollInterval  time.Duration
	BatchSize     int
	LeaseDuration time.Duration
	RetryBase     time.Duration
	RetryMax      time.Duration
}

type Worker struct {
	store     Store
	publisher Publisher
	logger    *slog.Logger
	config    WorkerConfig
	now       func() time.Time
	newID     func() (string, error)
}

const (
	defaultPollInterval  = time.Second
	defaultBatchSize     = 20
	defaultLeaseDuration = 30 * time.Second
	defaultRetryBase     = time.Second
	defaultRetryMax      = time.Minute
)

func NewWorker(
	store Store, publisher Publisher, logger *slog.Logger, config WorkerConfig,
) *Worker {
	if config.PollInterval <= 0 {
		config.PollInterval = defaultPollInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = defaultBatchSize
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
	return &Worker{
		store: store, publisher: publisher, logger: logger, config: config,
		now: time.Now, newID: randomLeaseID,
	}
}

// Run 保留单 Context 调用方式：取消时既停止领取新批次，也取消当前批次。
func (w *Worker) Run(ctx context.Context) {
	w.RunGracefully(ctx, ctx)
}

// RunGracefully 使用两个 Context 区分“停止领取新任务”和“强制取消在途任务”。
// stopCtx 取消后，当前已经认领的批次仍使用 workCtx 完成；只有 workCtx 也被取消时，
// 才中断正在进行的数据库或 Broker 操作。
func (w *Worker) RunGracefully(stopCtx, workCtx context.Context) {
	if w == nil || w.store == nil || w.publisher == nil {
		return
	}

	for {
		if stopCtx.Err() != nil || workCtx.Err() != nil {
			return
		}
		processed, err := w.RunOnce(workCtx)
		if err != nil && !errors.Is(err, context.Canceled) && w.logger != nil {
			w.logger.ErrorContext(workCtx, "outbox batch failed", "error", err)
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

// RunOnce 认领一批消息并逐条发布。发布成功后才确认；失败则安排指数退避重试。
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	leaseID, err := w.newID()
	if err != nil {
		return 0, fmt.Errorf("generate outbox lease ID: %w", err)
	}
	now := w.now().UTC()
	messages, err := w.store.Claim(
		ctx, leaseID, w.config.BatchSize, now, now.Add(w.config.LeaseDuration),
	)
	if err != nil {
		return 0, fmt.Errorf("claim outbox messages: %w", err)
	}

	var batchError error
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return len(messages), errors.Join(batchError, err)
		}
		messageContext := telemetry.Restore(ctx, message.TraceContext)
		messageContext, span := telemetry.Tracer("order-system/outbox").Start(
			messageContext, "outbox dispatch",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(
				attribute.String("messaging.system", "outbox"),
				attribute.String("messaging.destination.name", message.Topic),
				attribute.String("messaging.message.id", message.ID),
				attribute.Int("messaging.message.delivery_attempt", message.Attempts),
			),
		)
		if err := w.publisher.Publish(messageContext, message); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			reason := truncateError(err.Error(), 1000)
			nextAttemptAt := w.now().UTC().Add(w.retryDelay(message.Attempts))
			if markErr := w.store.MarkFailed(
				messageContext, message.ID, message.LeaseID, nextAttemptAt, reason,
			); markErr != nil {
				batchError = errors.Join(batchError, fmt.Errorf(
					"mark outbox message %q failed: %w", message.ID, markErr,
				))
			}
			if w.logger != nil {
				w.logger.WarnContext(messageContext, "outbox publish failed",
					"message_id", message.ID, "topic", message.Topic,
					"attempt", message.Attempts, "error", err,
				)
			}
			span.End()
			continue
		}

		if err := w.store.MarkPublished(
			messageContext, message.ID, message.LeaseID, w.now().UTC(),
		); err != nil {
			batchError = errors.Join(batchError, fmt.Errorf(
				"mark outbox message %q published: %w", message.ID, err,
			))
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}
	return len(messages), batchError
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

func truncateError(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
