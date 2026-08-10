package notification

import (
	"context"
	"fmt"
	"slices"
	"time"
)

const ScheduledMarketReport = "SCHEDULED_MARKET_REPORT"

type EnqueueRequest struct {
	IdempotencyKey string
	ScheduledFor   time.Time
	DataAsOf       time.Time
	Messages       []string
	ChatIDs        []string
	MaxAttempts    int
}

type EnqueueResult struct {
	OutboxID int64
	Created  bool
}

type Delivery struct {
	ChatID    string
	PartIndex int
	Status    string
}

type Job struct {
	OutboxID    int64
	Messages    []string
	Attempt     int
	MaxAttempts int
	Deliveries  []Delivery
}

type Repository interface {
	Enqueue(context.Context, EnqueueRequest) (EnqueueResult, error)
	ClaimDue(context.Context, time.Time) (Job, bool, error)
	RecoverStale(context.Context, time.Time) error
	MarkSending(context.Context, int64, string, int) error
	MarkSent(context.Context, int64, string, int, int64) error
	MarkFailed(context.Context, int64, string, int, string, bool) error
	Complete(context.Context, int64, time.Time) error
	Retry(context.Context, int64, time.Time, string) error
	Dead(context.Context, int64, string) error
	Unknown(context.Context, int64, string) error
}

type Enqueuer struct {
	repository Repository
	chatIDs    []string
}

func NewEnqueuer(repository Repository, chatIDs []string) (*Enqueuer, error) {
	if repository == nil || len(chatIDs) == 0 {
		return nil, fmt.Errorf("notification repository 和 chat ids 不能为空")
	}
	seen := make(map[string]struct{}, len(chatIDs))
	clean := make([]string, 0, len(chatIDs))
	for _, chatID := range chatIDs {
		if chatID == "" {
			return nil, fmt.Errorf("notification chat id 不能为空")
		}
		if _, exists := seen[chatID]; exists {
			continue
		}
		seen[chatID] = struct{}{}
		clean = append(clean, chatID)
	}
	slices.Sort(clean)
	return &Enqueuer{repository: repository, chatIDs: clean}, nil
}

func (e *Enqueuer) Enqueue(ctx context.Context, request EnqueueRequest) (EnqueueResult, error) {
	if request.IdempotencyKey == "" || request.ScheduledFor.IsZero() || request.DataAsOf.IsZero() ||
		request.DataAsOf.Before(request.ScheduledFor) || len(request.Messages) == 0 {
		return EnqueueResult{}, fmt.Errorf("notification enqueue request 无效")
	}
	if request.MaxAttempts <= 0 || request.MaxAttempts > 10 {
		return EnqueueResult{}, fmt.Errorf("notification max attempts 必须在 1 到 10 之间")
	}
	for index, message := range request.Messages {
		if message == "" {
			return EnqueueResult{}, fmt.Errorf("notification message %d 不能为空", index)
		}
	}
	request.ScheduledFor = request.ScheduledFor.UTC()
	request.DataAsOf = request.DataAsOf.UTC()
	request.ChatIDs = append([]string(nil), e.chatIDs...)
	return e.repository.Enqueue(ctx, request)
}
