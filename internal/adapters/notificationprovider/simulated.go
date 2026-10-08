// Package notificationprovider 提供通知 Provider 的基础设施适配器。
package notificationprovider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	notificationapp "example.com/order-system/internal/application/notification"
)

var ErrIdempotencyConflict = errors.New("notification provider idempotency conflict")

// Simulated 是本地学习用的通知 Provider。它模拟“向 recipient_id 发送通知”，
// 不访问真实邮件、短信或推送系统，并按幂等键保存结果。
type Simulated struct {
	mu                  sync.Mutex
	sent                map[string]recordedNotification
	delay               time.Duration
	failuresRemaining   int
	rejectionsRemaining int
}

type recordedNotification struct {
	request notificationapp.SendRequest
	result  notificationapp.SendResult
}

type Config struct {
	Delay      time.Duration
	Failures   int
	Rejections int
}

func NewSimulated() *Simulated { return NewSimulatedWithConfig(Config{}) }

func NewSimulatedWithConfig(config Config) *Simulated {
	return &Simulated{
		sent: make(map[string]recordedNotification), delay: config.Delay,
		failuresRemaining: config.Failures, rejectionsRemaining: config.Rejections,
	}
}

func (p *Simulated) Send(ctx context.Context, request notificationapp.SendRequest) (notificationapp.SendResult, error) {
	if err := ctx.Err(); err != nil {
		return notificationapp.SendResult{}, err
	}
	request.NotificationID = strings.TrimSpace(request.NotificationID)
	request.PaymentID = strings.TrimSpace(request.PaymentID)
	request.OrderID = strings.TrimSpace(request.OrderID)
	request.RecipientID = strings.TrimSpace(request.RecipientID)
	request.EventType = strings.TrimSpace(request.EventType)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.NotificationID == "" || request.PaymentID == "" || request.OrderID == "" ||
		request.RecipientID == "" || request.EventType == "" || request.IdempotencyKey == "" ||
		request.AmountCents <= 0 {
		return notificationapp.SendResult{}, errors.New("invalid simulated notification request")
	}
	if p.delay > 0 {
		timer := time.NewTimer(p.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return notificationapp.SendResult{}, ctx.Err()
		case <-timer.C:
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.sent[request.IdempotencyKey]; ok {
		if existing.request != request {
			return notificationapp.SendResult{}, ErrIdempotencyConflict
		}
		return existing.result, nil
	}
	if p.failuresRemaining > 0 {
		p.failuresRemaining--
		return notificationapp.SendResult{}, errors.New("simulated notification provider unavailable")
	}
	if p.rejectionsRemaining > 0 {
		p.rejectionsRemaining--
		return notificationapp.SendResult{Status: notificationapp.SendRejected, Reason: "simulated recipient rejected"}, nil
	}
	result := notificationapp.SendResult{
		Status:            notificationapp.SendSucceeded,
		ProviderReference: fmt.Sprintf("simulated-notification-%s", request.NotificationID),
	}
	p.sent[request.IdempotencyKey] = recordedNotification{request: request, result: result}
	return result, nil
}
