package messageops_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/order-system/internal/application/authorization"
	"example.com/order-system/internal/application/messageops"
)

type storeStub struct {
	action       messageops.RecoveryAction
	finishedWith string
	lastError    string
	beginErr     error
	retryCalls   int
}

func (s *storeStub) GetOutbox(context.Context, string) (messageops.OutboxRecord, error) {
	return messageops.OutboxRecord{}, nil
}
func (s *storeStub) RetryOutbox(_ context.Context, command messageops.RetryOutboxCommand) (messageops.RecoveryAction, error) {
	s.retryCalls++
	return messageops.RecoveryAction{ID: command.ActionID, Type: messageops.ActionOutboxRetry}, nil
}
func (s *storeStub) GetInbox(context.Context, string) (messageops.InboxRecord, error) {
	return messageops.InboxRecord{}, nil
}
func (s *storeStub) BeginDLQReplay(_ context.Context, action messageops.RecoveryAction) error {
	s.action = action
	return s.beginErr
}
func (s *storeStub) FinishDLQReplay(_ context.Context, _ string, status string, _ time.Time, lastError string) error {
	s.finishedWith, s.lastError = status, lastError
	return nil
}

type brokerStub struct {
	originalTopic string
	replayErr     error
	replays       int
}

func (b *brokerStub) ReadDeadLetter(_ context.Context, topic string, partition int32, offset int64) (messageops.DeadLetterMessage, error) {
	return messageops.DeadLetterMessage{
		DeadLetterTopic: topic, DeadLetterPartition: partition, DeadLetterOffset: offset,
		OriginalTopic: b.originalTopic,
	}, nil
}
func (b *brokerStub) ReplayDeadLetter(context.Context, messageops.DeadLetterMessage) error {
	b.replays++
	return b.replayErr
}

type idStub struct{ value string }

func (s idStub) NewID() (string, error) { return s.value, nil }

type actorAuthorizerStub struct{ err error }

func (s actorAuthorizerStub) Authorize(context.Context, string, authorization.Permission) error {
	return s.err
}

func TestRetryOutboxRequiresAuthorizedActorBeforeStoreMutation(t *testing.T) {
	store := &storeStub{}
	service := messageops.NewService(store, nil, idStub{"action-1"}, nil).
		WithActorAuthorization(actorAuthorizerStub{authorization.ErrPermissionDenied})
	_, err := service.RetryOutbox(context.Background(), "message-1", 2, "customer-1", "manual retry")
	if !errors.Is(err, authorization.ErrPermissionDenied) {
		t.Fatalf("RetryOutbox() error = %v", err)
	}
	if store.retryCalls != 0 {
		t.Fatalf("unauthorized retry reached store %d times", store.retryCalls)
	}
}

func TestReplayDeadLetterRequiresAllowedOriginalTopic(t *testing.T) {
	store := &storeStub{}
	broker := &brokerStub{originalTopic: "payments.succeeded.v1"}
	service := messageops.NewService(store, broker, idStub{"action-1"}, []string{"orders.created.v1"})

	_, err := service.ReplayDeadLetter(context.Background(), "orders.created.v1.dlq", 0, 12, "operator-1", "fixed schema")
	if !errors.Is(err, messageops.ErrReplayTopicDenied) {
		t.Fatalf("ReplayDeadLetter() error = %v, want topic denied", err)
	}
	if broker.replays != 0 || store.action.ID != "" {
		t.Fatalf("denied replay reached side effects: replays=%d action=%+v", broker.replays, store.action)
	}
}

func TestReplayDeadLetterRecordsUnknownOutcomeWithoutAutomaticRetry(t *testing.T) {
	store := &storeStub{}
	brokerErr := errors.New("broker response lost")
	broker := &brokerStub{originalTopic: "orders.created.v1", replayErr: brokerErr}
	service := messageops.NewService(store, broker, idStub{"action-1"}, []string{"orders.created.v1"})

	action, err := service.ReplayDeadLetter(context.Background(), "orders.created.v1.dlq", 2, 42, "operator-1", "consumer fixed")
	if err == nil || action.Status != messageops.StatusUnknown {
		t.Fatalf("ReplayDeadLetter() = %+v, %v; want unknown error", action, err)
	}
	if broker.replays != 1 || store.finishedWith != messageops.StatusUnknown || store.lastError == "" {
		t.Fatalf("replay state = replays %d, status %q, error %q", broker.replays, store.finishedWith, store.lastError)
	}
}

func TestReplayDeadLetterCompletesRecordedAction(t *testing.T) {
	store := &storeStub{}
	broker := &brokerStub{originalTopic: "orders.created.v1"}
	service := messageops.NewService(store, broker, idStub{"action-1"}, []string{"orders.created.v1"})

	action, err := service.ReplayDeadLetter(context.Background(), "orders.created.v1.dlq", 1, 9, "operator-1", "consumer fixed")
	if err != nil {
		t.Fatalf("ReplayDeadLetter() error = %v", err)
	}
	if action.Status != messageops.StatusCompleted || store.finishedWith != messageops.StatusCompleted {
		t.Fatalf("ReplayDeadLetter() action = %+v, persisted status = %q", action, store.finishedWith)
	}
}
