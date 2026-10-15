// Package messageops 提供消息故障的受控人工恢复用例。
package messageops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"example.com/order-system/internal/application/authorization"
)

var (
	ErrOutboxNotFound       = errors.New("outbox message not found")
	ErrInboxNotFound        = errors.New("inbox message not found")
	ErrOutboxPublished      = errors.New("outbox message is already published")
	ErrOutboxLeaseActive    = errors.New("outbox message has an active lease")
	ErrOutboxStateChanged   = errors.New("outbox message state changed")
	ErrReplayAlreadyStarted = errors.New("dead-letter replay was already started")
	ErrReplayTopicDenied    = errors.New("dead-letter original topic is not allowed")
)

const (
	ActionOutboxRetry = "outbox_retry"
	ActionDLQReplay   = "dlq_replay"
	StatusRequested   = "requested"
	StatusCompleted   = "completed"
	StatusUnknown     = "unknown"
)

type OutboxRecord struct {
	ID            string     `json:"id"`
	Topic         string     `json:"topic"`
	MessageKey    string     `json:"message_key"`
	Attempts      int        `json:"attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LockedUntil   *time.Time `json:"locked_until,omitempty"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	HasLastError  bool       `json:"has_last_error"`
}

type InboxRecord struct {
	MessageID   string    `json:"message_id"`
	Topic       string    `json:"topic"`
	PayloadHash string    `json:"payload_hash"`
	ProcessedAt time.Time `json:"processed_at"`
}

type RecoveryAction struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Target    string    `json:"target"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type RetryOutboxCommand struct {
	ActionID         string
	MessageID        string
	ExpectedAttempts int
	Actor            string
	Reason           string
	Now              time.Time
}

type Header struct {
	Key   string
	Value []byte
}

type DeadLetterMessage struct {
	DeadLetterTopic     string
	DeadLetterPartition int32
	DeadLetterOffset    int64
	OriginalTopic       string
	Key                 []byte
	Value               []byte
	Headers             []Header
}

func (m DeadLetterMessage) Target() string {
	return fmt.Sprintf("%s/%d/%d", m.DeadLetterTopic, m.DeadLetterPartition, m.DeadLetterOffset)
}

type Store interface {
	GetOutbox(context.Context, string) (OutboxRecord, error)
	RetryOutbox(context.Context, RetryOutboxCommand) (RecoveryAction, error)
	GetInbox(context.Context, string) (InboxRecord, error)
	BeginDLQReplay(context.Context, RecoveryAction) error
	FinishDLQReplay(context.Context, string, string, time.Time, string) error
}

type DeadLetterBroker interface {
	ReadDeadLetter(context.Context, string, int32, int64) (DeadLetterMessage, error)
	ReplayDeadLetter(context.Context, DeadLetterMessage) error
}

type IDGenerator interface {
	NewID() (string, error)
}

type Service struct {
	store         Store
	broker        DeadLetterBroker
	ids           IDGenerator
	now           func() time.Time
	allowedTopics map[string]struct{}
	authorizer    interface {
		Authorize(context.Context, string, authorization.Permission) error
	}
}

func (s *Service) WithActorAuthorization(authorizer interface {
	Authorize(context.Context, string, authorization.Permission) error
}) *Service {
	s.authorizer = authorizer
	return s
}

func NewService(store Store, broker DeadLetterBroker, ids IDGenerator, allowedTopics []string) *Service {
	allowed := make(map[string]struct{}, len(allowedTopics))
	for _, topic := range allowedTopics {
		if topic = strings.TrimSpace(topic); topic != "" {
			allowed[topic] = struct{}{}
		}
	}
	return &Service{store: store, broker: broker, ids: ids, now: time.Now, allowedTopics: allowed}
}

func (s *Service) InspectOutbox(ctx context.Context, messageID string) (OutboxRecord, error) {
	if s == nil || s.store == nil || strings.TrimSpace(messageID) == "" {
		return OutboxRecord{}, ErrOutboxNotFound
	}
	return s.store.GetOutbox(ctx, strings.TrimSpace(messageID))
}

func (s *Service) InspectInbox(ctx context.Context, messageID string) (InboxRecord, error) {
	if s == nil || s.store == nil || strings.TrimSpace(messageID) == "" {
		return InboxRecord{}, ErrInboxNotFound
	}
	return s.store.GetInbox(ctx, strings.TrimSpace(messageID))
}

func (s *Service) RetryOutbox(
	ctx context.Context, messageID string, expectedAttempts int, actor, reason string,
) (RecoveryAction, error) {
	if s == nil || s.store == nil || s.ids == nil {
		return RecoveryAction{}, errors.New("message operations service is not configured")
	}
	actionID, err := s.ids.NewID()
	if err != nil {
		return RecoveryAction{}, fmt.Errorf("generate recovery action ID: %w", err)
	}
	command := RetryOutboxCommand{
		ActionID: actionID, MessageID: strings.TrimSpace(messageID),
		ExpectedAttempts: expectedAttempts, Actor: strings.TrimSpace(actor),
		Reason: strings.TrimSpace(reason), Now: s.now().UTC(),
	}
	if command.MessageID == "" || command.ExpectedAttempts < 0 || command.Actor == "" || command.Reason == "" {
		return RecoveryAction{}, errors.New("invalid outbox retry request")
	}
	if s.authorizer != nil {
		if err := s.authorizer.Authorize(ctx, command.Actor, authorization.PermissionMessageRecovery); err != nil {
			return RecoveryAction{}, err
		}
	}
	return s.store.RetryOutbox(ctx, command)
}

func (s *Service) ReplayDeadLetter(
	ctx context.Context, deadLetterTopic string, partition int32, offset int64, actor, reason string,
) (RecoveryAction, error) {
	if s == nil || s.store == nil || s.broker == nil || s.ids == nil {
		return RecoveryAction{}, errors.New("dead-letter replay service is not configured")
	}
	deadLetterTopic, actor, reason = strings.TrimSpace(deadLetterTopic), strings.TrimSpace(actor), strings.TrimSpace(reason)
	if deadLetterTopic == "" || partition < 0 || offset < 0 || actor == "" || reason == "" {
		return RecoveryAction{}, errors.New("invalid dead-letter replay request")
	}
	if s.authorizer != nil {
		if err := s.authorizer.Authorize(ctx, actor, authorization.PermissionMessageRecovery); err != nil {
			return RecoveryAction{}, err
		}
	}
	message, err := s.broker.ReadDeadLetter(ctx, deadLetterTopic, partition, offset)
	if err != nil {
		return RecoveryAction{}, fmt.Errorf("read dead-letter record: %w", err)
	}
	if _, allowed := s.allowedTopics[message.OriginalTopic]; !allowed {
		return RecoveryAction{}, fmt.Errorf("%w: %s", ErrReplayTopicDenied, message.OriginalTopic)
	}
	actionID, err := s.ids.NewID()
	if err != nil {
		return RecoveryAction{}, fmt.Errorf("generate recovery action ID: %w", err)
	}
	action := RecoveryAction{
		ID: actionID, Type: ActionDLQReplay, Target: message.Target(),
		Actor: actor, Reason: reason, Status: StatusRequested, CreatedAt: s.now().UTC(),
	}
	if err := s.store.BeginDLQReplay(ctx, action); err != nil {
		return RecoveryAction{}, err
	}
	if err := s.broker.ReplayDeadLetter(ctx, message); err != nil {
		// Broker 超时不能证明消息未写入，因此记录 unknown，禁止自动再次重放。
		finishedAt := s.now().UTC()
		if finishErr := s.store.FinishDLQReplay(ctx, action.ID, StatusUnknown, finishedAt, err.Error()); finishErr != nil {
			return RecoveryAction{}, errors.Join(err, finishErr)
		}
		action.Status = StatusUnknown
		return action, fmt.Errorf("replay outcome is unknown: %w", err)
	}
	if err := s.store.FinishDLQReplay(ctx, action.ID, StatusCompleted, s.now().UTC(), ""); err != nil {
		return RecoveryAction{}, fmt.Errorf("record completed dead-letter replay: %w", err)
	}
	action.Status = StatusCompleted
	return action, nil
}
