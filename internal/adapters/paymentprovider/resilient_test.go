package paymentprovider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	paymentapp "example.com/order-system/internal/application/payment"
)

type providerFunc func(context.Context, paymentapp.ChargeRequest) (paymentapp.ChargeResult, error)

func (f providerFunc) Charge(
	ctx context.Context, request paymentapp.ChargeRequest,
) (paymentapp.ChargeResult, error) {
	return f(ctx, request)
}

type resilienceMetricsStub struct {
	mu          sync.Mutex
	calls       []string
	rejections  []string
	states      []string
	transitions []string
}

func (m *resilienceMetricsStub) RecordPaymentProviderCall(outcome string, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, outcome)
}

func (m *resilienceMetricsStub) RecordPaymentProviderRejection(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejections = append(m.rejections, reason)
}

func (m *resilienceMetricsStub) SetPaymentProviderCircuitState(state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, state)
}

func (m *resilienceMetricsStub) RecordPaymentProviderCircuitTransition(state string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.transitions = append(m.transitions, state)
}

func TestResilientProviderTimesOutAStalledCall(t *testing.T) {
	metrics := &resilienceMetricsStub{}
	provider := NewResilient(providerFunc(func(
		ctx context.Context, _ paymentapp.ChargeRequest,
	) (paymentapp.ChargeResult, error) {
		<-ctx.Done()
		return paymentapp.ChargeResult{}, ctx.Err()
	}), ResilienceConfig{
		Timeout: 20 * time.Millisecond, MaxConcurrent: 1,
		FailureThreshold: 3, OpenDuration: time.Minute,
	}, metrics)

	started := time.Now()
	_, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{})

	if !errors.Is(err, ErrCallTimeout) {
		t.Fatalf("Charge() error = %v, want ErrCallTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("timed out call took %s", elapsed)
	}
	if len(metrics.calls) != 1 || metrics.calls[0] != "timeout" {
		t.Fatalf("call outcomes = %v, want [timeout]", metrics.calls)
	}
}

func TestResilientProviderRejectsLateSuccessFromProviderIgnoringContext(t *testing.T) {
	provider := NewResilient(providerFunc(func(
		context.Context, paymentapp.ChargeRequest,
	) (paymentapp.ChargeResult, error) {
		time.Sleep(30 * time.Millisecond)
		return paymentapp.ChargeResult{Status: paymentapp.ChargeSucceeded}, nil
	}), ResilienceConfig{
		Timeout: 10 * time.Millisecond, MaxConcurrent: 1,
		FailureThreshold: 3, OpenDuration: time.Minute,
	}, nil)

	result, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{})

	if !errors.Is(err, ErrCallTimeout) {
		t.Fatalf("Charge() error = %v, want ErrCallTimeout", err)
	}
	if result != (paymentapp.ChargeResult{}) {
		t.Fatalf("Charge() result = %+v, want zero result for a timed-out call", result)
	}
}

func TestResilientProviderRejectsLateSuccessAfterCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var signalStarted sync.Once
	provider := NewResilient(providerFunc(func(
		context.Context, paymentapp.ChargeRequest,
	) (paymentapp.ChargeResult, error) {
		signalStarted.Do(func() { close(started) })
		<-release
		return paymentapp.ChargeResult{Status: paymentapp.ChargeSucceeded}, nil
	}), ResilienceConfig{
		Timeout: time.Second, MaxConcurrent: 1,
		FailureThreshold: 1, OpenDuration: time.Minute,
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := provider.Charge(ctx, paymentapp.ChargeRequest{})
		done <- err
	}()
	<-started
	cancel()
	close(release)

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Charge() error = %v, want context canceled", err)
	}
	if _, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{}); err != nil {
		t.Fatalf("caller cancellation tripped circuit: %v", err)
	}
}

func TestResilientProviderRejectsCallsBeyondConcurrencyLimit(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	metrics := &resilienceMetricsStub{}
	provider := NewResilient(providerFunc(func(
		context.Context, paymentapp.ChargeRequest,
	) (paymentapp.ChargeResult, error) {
		close(entered)
		<-release
		return paymentapp.ChargeResult{Status: paymentapp.ChargeSucceeded}, nil
	}), ResilienceConfig{
		Timeout: time.Second, MaxConcurrent: 1,
		FailureThreshold: 3, OpenDuration: time.Minute,
	}, metrics)

	firstDone := make(chan error, 1)
	go func() {
		_, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{})
		firstDone <- err
	}()
	<-entered

	_, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{})
	if !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("second Charge() error = %v, want ErrConcurrencyLimit", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Charge() error = %v", err)
	}
	if len(metrics.rejections) != 1 || metrics.rejections[0] != "concurrency_limit" {
		t.Fatalf("rejections = %v, want [concurrency_limit]", metrics.rejections)
	}
}

func TestResilientProviderOpensAndRecoversCircuit(t *testing.T) {
	providerCalls := 0
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	metrics := &resilienceMetricsStub{}
	provider := NewResilient(providerFunc(func(
		context.Context, paymentapp.ChargeRequest,
	) (paymentapp.ChargeResult, error) {
		providerCalls++
		if providerCalls <= 2 {
			return paymentapp.ChargeResult{}, errors.New("provider unavailable")
		}
		if providerCalls == 3 {
			close(probeEntered)
			<-releaseProbe
		}
		return paymentapp.ChargeResult{Status: paymentapp.ChargeSucceeded}, nil
	}), ResilienceConfig{
		Timeout: time.Second, MaxConcurrent: 2,
		FailureThreshold: 2, OpenDuration: time.Minute,
	}, metrics)
	provider.now = func() time.Time { return now }

	for attempt := 0; attempt < 2; attempt++ {
		if _, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{}); err == nil {
			t.Fatalf("failure %d error = nil", attempt+1)
		}
	}
	if _, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("open-circuit Charge() error = %v, want ErrCircuitOpen", err)
	}
	if providerCalls != 2 {
		t.Fatalf("provider calls while open = %d, want 2", providerCalls)
	}

	now = now.Add(time.Minute)
	probeDone := make(chan error, 1)
	go func() {
		_, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{})
		probeDone <- err
	}()
	<-probeEntered
	if _, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("second half-open Charge() error = %v, want ErrCircuitOpen", err)
	}
	close(releaseProbe)
	if err := <-probeDone; err != nil {
		t.Fatalf("probe Charge() error = %v", err)
	}
	if _, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{}); err != nil {
		t.Fatalf("closed-circuit Charge() error = %v", err)
	}
	if providerCalls != 4 {
		t.Fatalf("provider calls after recovery = %d, want 4", providerCalls)
	}
	wantTransitions := []string{"open", "half_open", "closed"}
	if len(metrics.transitions) != len(wantTransitions) {
		t.Fatalf("transitions = %v, want %v", metrics.transitions, wantTransitions)
	}
	for index := range wantTransitions {
		if metrics.transitions[index] != wantTransitions[index] {
			t.Fatalf("transitions = %v, want %v", metrics.transitions, wantTransitions)
		}
	}
}

func TestResilientProviderDoesNotTripForBusinessDecline(t *testing.T) {
	providerCalls := 0
	provider := NewResilient(providerFunc(func(
		context.Context, paymentapp.ChargeRequest,
	) (paymentapp.ChargeResult, error) {
		providerCalls++
		return paymentapp.ChargeResult{Status: paymentapp.ChargeDeclined}, nil
	}), ResilienceConfig{
		Timeout: time.Second, MaxConcurrent: 1,
		FailureThreshold: 1, OpenDuration: time.Minute,
	}, nil)

	for range 2 {
		result, err := provider.Charge(context.Background(), paymentapp.ChargeRequest{})
		if err != nil || result.Status != paymentapp.ChargeDeclined {
			t.Fatalf("Charge() = %+v, %v", result, err)
		}
	}
	if providerCalls != 2 {
		t.Fatalf("provider calls = %d, want 2", providerCalls)
	}
}

func TestCircuitIgnoresResultFromGenerationBeforeRecovery(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	breaker := circuitBreaker{
		state: circuitClosed, failureThreshold: 1, openDuration: time.Minute,
	}
	stale := breaker.before(now)
	if transition := breaker.after(now, breakerFailed, stale); transition != circuitOpen {
		t.Fatalf("failure transition = %q, want open", transition)
	}

	probe := breaker.before(now.Add(time.Minute))
	if transition := breaker.after(now.Add(time.Minute), breakerSucceeded, probe); transition != circuitClosed {
		t.Fatalf("probe transition = %q, want closed", transition)
	}
	if transition := breaker.after(now.Add(time.Minute), breakerFailed, stale); transition != "" {
		t.Fatalf("stale transition = %q, want empty", transition)
	}

	fresh := breaker.before(now.Add(time.Minute))
	if transition := breaker.after(now.Add(time.Minute), breakerFailed, fresh); transition != circuitOpen {
		t.Fatalf("fresh failure transition = %q, want open", transition)
	}
}

func TestCircuitReopensWhenHalfOpenProbeFails(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	breaker := circuitBreaker{
		state: circuitClosed, failureThreshold: 1, openDuration: time.Minute,
	}
	first := breaker.before(now)
	if transition := breaker.after(now, breakerFailed, first); transition != circuitOpen {
		t.Fatalf("failure transition = %q, want open", transition)
	}

	probe := breaker.before(now.Add(time.Minute))
	if !probe.allowed || !probe.probe || probe.transition != circuitHalfOpen {
		t.Fatalf("probe permission = %+v", probe)
	}
	if transition := breaker.after(now.Add(time.Minute), breakerFailed, probe); transition != circuitOpen {
		t.Fatalf("probe failure transition = %q, want open", transition)
	}
	if permission := breaker.before(now.Add(2*time.Minute - time.Nanosecond)); permission.allowed {
		t.Fatalf("permission before renewed cooldown = %+v, want rejected", permission)
	}
}
