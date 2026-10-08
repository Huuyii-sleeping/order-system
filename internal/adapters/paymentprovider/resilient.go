package paymentprovider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	paymentapp "example.com/order-system/internal/application/payment"
)

var (
	// ErrCallTimeout 表示本次调用超过了调用方为支付 Provider 设置的时间预算。
	// 超时无法证明第三方没有扣款，Worker 必须把支付保留在 unknown 并使用原幂等键重试。
	ErrCallTimeout = errors.New("payment provider call timed out")

	// ErrConcurrencyLimit 表示本进程允许的并发支付调用已经用完。快速拒绝可以避免
	// 请求在内存中无限排队，但它同样不代表支付业务失败。
	ErrConcurrencyLimit = errors.New("payment provider concurrency limit reached")

	// ErrCircuitOpen 表示近期下游故障已经达到阈值，熔断器暂时阻止真实调用。
	ErrCircuitOpen = errors.New("payment provider circuit is open")
)

const (
	defaultProviderTimeout         = 2 * time.Second
	defaultProviderMaxConcurrent   = 4
	defaultCircuitFailureThreshold = 5
	defaultCircuitOpenDuration     = 30 * time.Second

	circuitClosed   = "closed"
	circuitOpen     = "open"
	circuitHalfOpen = "half_open"

	breakerIgnored = iota
	breakerSucceeded
	breakerFailed
)

type circuitPermission struct {
	allowed    bool
	probe      bool
	generation uint64
	transition string
}

type ResilienceConfig struct {
	Timeout          time.Duration
	MaxConcurrent    int
	FailureThreshold int
	OpenDuration     time.Duration
}

// ResilienceMetrics 只暴露支付稳定性装饰器需要的低基数指标。
// 业务 ID 不进入标签，否则每笔支付都会创建新的 Prometheus 时间序列。
type ResilienceMetrics interface {
	RecordPaymentProviderCall(outcome string, duration time.Duration)
	RecordPaymentProviderRejection(reason string)
	SetPaymentProviderCircuitState(state string)
	RecordPaymentProviderCircuitTransition(state string)
}

// Resilient 在具体支付适配器外增加超时、并发舱壁和熔断。它仍然实现应用层
// Provider 端口，所以 Worker 不需要知道底层使用 HTTP、SDK 还是本地模拟器。
type Resilient struct {
	next      paymentapp.Provider
	timeout   time.Duration
	semaphore chan struct{}
	breaker   circuitBreaker
	// transitionMu 让熔断状态转换与对应 Gauge 更新保持同一顺序，避免并发
	// 调用把较旧的状态最后写入监控。它不会覆盖真实 Provider 网络调用。
	transitionMu sync.Mutex
	metrics      ResilienceMetrics
	now          func() time.Time
}

func NewResilient(
	next paymentapp.Provider, config ResilienceConfig, metrics ResilienceMetrics,
) *Resilient {
	config = withResilienceDefaults(config)
	provider := &Resilient{
		next: next, timeout: config.Timeout,
		semaphore: make(chan struct{}, config.MaxConcurrent),
		breaker: circuitBreaker{
			state: circuitClosed, failureThreshold: config.FailureThreshold,
			openDuration: config.OpenDuration,
		},
		metrics: metrics, now: time.Now,
	}
	provider.setCircuitState(circuitClosed, false)
	return provider
}

func (p *Resilient) Charge(
	ctx context.Context, request paymentapp.ChargeRequest,
) (paymentapp.ChargeResult, error) {
	if p == nil || p.next == nil {
		return paymentapp.ChargeResult{}, errors.New("payment provider is not configured")
	}

	// 舱壁使用非阻塞获取：容量耗尽时尽快把任务交回持久化重试队列，
	// 不在 Go 进程内堆积一条不可观测、重启即丢失的等待队列。
	select {
	case p.semaphore <- struct{}{}:
		defer func() { <-p.semaphore }()
	default:
		p.recordRejection("concurrency_limit")
		return paymentapp.ChargeResult{}, ErrConcurrencyLimit
	}

	p.transitionMu.Lock()
	permission := p.breaker.before(p.now())
	if permission.transition != "" {
		p.setCircuitState(permission.transition, true)
	}
	p.transitionMu.Unlock()
	if !permission.allowed {
		p.recordRejection("circuit_open")
		return paymentapp.ChargeResult{}, ErrCircuitOpen
	}

	started := time.Now()
	callContext, cancel := context.WithTimeout(ctx, p.timeout)
	result, err := p.next.Charge(callContext, request)
	callContextErr := callContext.Err()
	cancel()

	outcome, breakerResult, returnedErr := classifyCall(ctx.Err(), callContextErr, result, err)
	p.transitionMu.Lock()
	transition := p.breaker.after(p.now(), breakerResult, permission)
	if transition != "" {
		p.setCircuitState(transition, true)
	}
	p.transitionMu.Unlock()
	p.recordCall(outcome, time.Since(started))
	if returnedErr != nil {
		return paymentapp.ChargeResult{}, returnedErr
	}
	return result, returnedErr
}

func classifyCall(
	parentErr, callContextErr error, result paymentapp.ChargeResult, err error,
) (outcome string, breakerResult int, returnedErr error) {
	// 即使 SDK 没有遵守 Context 并在截止时间后返回成功，也不能采信这份迟到响应。
	// 调用方已经把该次尝试视为超时，只能保守地按 unknown 重试和对账。
	if parentErr == nil && errors.Is(callContextErr, context.DeadlineExceeded) {
		if err == nil {
			err = callContextErr
		}
		return "timeout", breakerFailed, fmt.Errorf("%w: %v", ErrCallTimeout, err)
	}
	if parentErr != nil {
		// 调用方关闭服务不能证明支付依赖故障；即使 SDK 迟到返回成功，也不能
		// 在已经取消的任务上下文中采信结果。
		return "canceled", breakerIgnored, parentErr
	}
	if err != nil {
		return "error", breakerFailed, err
	}

	switch result.Status {
	case paymentapp.ChargeSucceeded:
		return "success", breakerSucceeded, nil
	case paymentapp.ChargeDeclined:
		// 业务拒绝是一个明确且健康的 Provider 响应，不能因为余额不足等业务结果熔断。
		return "declined", breakerSucceeded, nil
	default:
		// 保留原结果让 Worker 按 unknown 处理，同时把协议异常计入熔断。
		return "invalid_response", breakerFailed, nil
	}
}

func withResilienceDefaults(config ResilienceConfig) ResilienceConfig {
	if config.Timeout <= 0 {
		config.Timeout = defaultProviderTimeout
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = defaultProviderMaxConcurrent
	}
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = defaultCircuitFailureThreshold
	}
	if config.OpenDuration <= 0 {
		config.OpenDuration = defaultCircuitOpenDuration
	}
	return config
}

func (p *Resilient) recordCall(outcome string, duration time.Duration) {
	if p.metrics != nil {
		p.metrics.RecordPaymentProviderCall(outcome, duration)
	}
}

func (p *Resilient) recordRejection(reason string) {
	if p.metrics != nil {
		p.metrics.RecordPaymentProviderRejection(reason)
	}
}

func (p *Resilient) setCircuitState(state string, transition bool) {
	if p.metrics == nil {
		return
	}
	p.metrics.SetPaymentProviderCircuitState(state)
	if transition {
		p.metrics.RecordPaymentProviderCircuitTransition(state)
	}
}

type circuitBreaker struct {
	mu               sync.Mutex
	state            string
	failures         int
	failureThreshold int
	openDuration     time.Duration
	openedAt         time.Time
	generation       uint64
}

func (b *circuitBreaker) before(now time.Time) circuitPermission {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case circuitOpen:
		if now.Sub(b.openedAt) < b.openDuration {
			return circuitPermission{}
		}
		b.state = circuitHalfOpen
		b.generation++
		return circuitPermission{
			allowed: true, probe: true, generation: b.generation, transition: circuitHalfOpen,
		}
	case circuitHalfOpen:
		// 半开阶段只允许一个探测请求，防止恢复瞬间重新压垮下游。
		return circuitPermission{}
	default:
		return circuitPermission{allowed: true, generation: b.generation}
	}
}

func (b *circuitBreaker) after(
	now time.Time, result int, permission circuitPermission,
) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 状态转换后会进入新代次。旧代次中较慢的并发调用即使现在才返回，
	// 也不能覆盖已经完成的半开探测结果或污染新一轮连续失败计数。
	if permission.generation != b.generation {
		return ""
	}
	if permission.probe {
		if b.state != circuitHalfOpen {
			return ""
		}
		if result != breakerSucceeded {
			b.state = circuitOpen
			b.openedAt = now
			b.generation++
			return circuitOpen
		}
		b.state = circuitClosed
		b.failures = 0
		b.generation++
		return circuitClosed
	}

	if b.state != circuitClosed || result == breakerIgnored {
		return ""
	}
	if result == breakerSucceeded {
		b.failures = 0
		return ""
	}
	b.failures++
	if b.failures < b.failureThreshold {
		return ""
	}
	b.state = circuitOpen
	b.openedAt = now
	b.failures = 0
	b.generation++
	return circuitOpen
}
