package authapp

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// CredentialCleaner 是认证维护 Worker 依赖的最小应用端口。
// Service 实现这个端口，Worker 不需要知道认证存储是内存还是 PostgreSQL。
type CredentialCleaner interface {
	PruneExpiredCredentials(context.Context, time.Time, int) (CleanupResult, error)
}

// CleanupMetrics 是维护 Worker 的观测端口。应用层只报告固定结果和删除数量，
// 具体由 Prometheus、测试桩或其他监控适配器实现。
type CleanupMetrics interface {
	RecordAuthCredentialCleanup(outcome string, sessionsDeleted, refreshTokensDeleted int64, duration time.Duration)
}

type CleanupWorkerConfig struct {
	Interval  time.Duration
	BatchSize int
}

const (
	defaultCleanupInterval  = time.Hour
	defaultCleanupBatchSize = 500
)

// CleanupWorker 周期性物理删除已过期的 Access Session 和 Refresh Token。
// 它是维护任务，不参与请求链路，也不应该因为一次数据库临时故障退出整个服务。
type CleanupWorker struct {
	cleaner CredentialCleaner
	clock   Clock
	logger  *slog.Logger
	metrics CleanupMetrics
	config  CleanupWorkerConfig
}

func NewCleanupWorker(
	cleaner CredentialCleaner,
	clock Clock,
	logger *slog.Logger,
	config CleanupWorkerConfig,
	metrics CleanupMetrics,
) *CleanupWorker {
	if config.Interval <= 0 {
		config.Interval = defaultCleanupInterval
	}
	if config.BatchSize <= 0 {
		config.BatchSize = defaultCleanupBatchSize
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &CleanupWorker{
		cleaner: cleaner,
		clock:   clock,
		logger:  logger,
		metrics: metrics,
		config:  config,
	}
}

// RunGracefully 首次启动立即做一批清理，之后按 Interval 轮询。
// stopCtx 只停止未来的清理批次；当前数据库操作继续使用 workCtx，
// 直到关闭预算耗尽时由 Runtime 取消 workCtx。
func (w *CleanupWorker) RunGracefully(stopCtx, workCtx context.Context) {
	if w == nil || w.cleaner == nil || w.clock == nil {
		return
	}
	if stopCtx.Err() != nil || workCtx.Err() != nil {
		return
	}

	w.runOnce(workCtx)
	ticker := time.NewTicker(w.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-stopCtx.Done():
			return
		case <-workCtx.Done():
			return
		case <-ticker.C:
			if stopCtx.Err() != nil || workCtx.Err() != nil {
				return
			}
			w.runOnce(workCtx)
		}
	}
}

// RunOnce 暴露单批处理入口，便于集成测试和手工验收维护 SQL。
func (w *CleanupWorker) RunOnce(ctx context.Context) (CleanupResult, error) {
	if w == nil || w.cleaner == nil || w.clock == nil {
		return CleanupResult{}, errors.New("credential cleanup worker is not configured")
	}
	if err := ctx.Err(); err != nil {
		return CleanupResult{}, err
	}
	return w.cleaner.PruneExpiredCredentials(ctx, w.clock.Now(), w.config.BatchSize)
}

func (w *CleanupWorker) runOnce(ctx context.Context) {
	startedAt := time.Now()
	result, err := w.RunOnce(ctx)
	if w.metrics != nil {
		outcome := "success"
		if err != nil {
			outcome = "error"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				outcome = "canceled"
			}
		}
		w.metrics.RecordAuthCredentialCleanup(
			outcome, result.SessionsDeleted, result.RefreshTokensDeleted, time.Since(startedAt),
		)
	}
	if err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && w.logger != nil {
			w.logger.ErrorContext(ctx, "expired credential cleanup failed", "error", err)
		}
		return
	}
	if w.logger != nil && (result.SessionsDeleted > 0 || result.RefreshTokensDeleted > 0) {
		w.logger.InfoContext(ctx, "expired credentials cleaned",
			"sessions_deleted", result.SessionsDeleted,
			"refresh_tokens_deleted", result.RefreshTokensDeleted,
			"batch_size", w.config.BatchSize,
		)
	}
}

// systemClock 只用于 Worker 未显式注入时的生产默认值；测试可以注入固定时钟。
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

var _ CredentialCleaner = (*Service)(nil)
