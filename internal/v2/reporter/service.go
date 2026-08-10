package reporter

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"binance-monitor/internal/notification"
	v2report "binance-monitor/internal/v2/report"
)

type ReportBuilder interface {
	Messages(context.Context) (v2report.Snapshot, []string, error)
}

type Enqueuer interface {
	Enqueue(context.Context, notification.EnqueueRequest) (notification.EnqueueResult, error)
}

type Dispatcher interface {
	Recover(context.Context, time.Time) error
	DispatchOne(context.Context, time.Time) (bool, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type Service struct {
	reports     ReportBuilder
	enqueuer    Enqueuer
	dispatcher  Dispatcher
	clock       Clock
	location    *time.Location
	hours       []int
	grace       time.Duration
	pollEvery   time.Duration
	maxAttempts int
	logger      *slog.Logger
}

type CycleResult struct {
	Slot       time.Time
	Enqueued   bool
	Created    bool
	Dispatched int
}

func New(
	reports ReportBuilder,
	enqueuer Enqueuer,
	dispatcher Dispatcher,
	clock Clock,
	location *time.Location,
	hours []int,
	grace time.Duration,
	pollEvery time.Duration,
	maxAttempts int,
	logger *slog.Logger,
) (*Service, error) {
	if reports == nil || enqueuer == nil || dispatcher == nil || clock == nil || location == nil || logger == nil ||
		len(hours) == 0 || grace <= 0 || grace >= time.Hour || pollEvery <= 0 || maxAttempts <= 0 || maxAttempts > 10 {
		return nil, fmt.Errorf("V2 reporter 配置或依赖无效")
	}
	for _, hour := range hours {
		if hour < 0 || hour > 23 {
			return nil, fmt.Errorf("V2 reporter hour 无效: %d", hour)
		}
	}
	return &Service{
		reports: reports, enqueuer: enqueuer, dispatcher: dispatcher, clock: clock,
		location: location, hours: append([]int(nil), hours...), grace: grace,
		pollEvery: pollEvery, maxAttempts: maxAttempts, logger: logger,
	}, nil
}

func (s *Service) Run(ctx context.Context) error {
	for {
		result, err := s.RunCycle(ctx)
		if err != nil {
			s.logger.Error("V2 reporter cycle 失败", "error", err)
		} else if result.Enqueued || result.Dispatched > 0 {
			s.logger.Info(
				"V2 reporter cycle 完成",
				"slot", result.Slot,
				"enqueued", result.Enqueued,
				"created", result.Created,
				"dispatched", result.Dispatched,
			)
		}
		timer := time.NewTimer(s.pollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (s *Service) RunCycle(ctx context.Context) (CycleResult, error) {
	now := s.clock.Now()
	result := CycleResult{}
	if err := s.dispatcher.Recover(ctx, now); err != nil {
		return result, fmt.Errorf("恢复 notification outbox: %w", err)
	}
	if slot, due := DueSlot(now.In(s.location), s.hours, s.grace); due {
		result.Slot = slot
		snapshot, messages, err := s.reports.Messages(ctx)
		if err != nil {
			return result, err
		}
		slotUTC := slot.UTC()
		if snapshot.AsOf.Before(slotUTC) {
			return result, fmt.Errorf("报告数据尚未到达 slot：slot=%s as_of=%s", slotUTC, snapshot.AsOf)
		}
		enqueue, err := s.enqueuer.Enqueue(ctx, notification.EnqueueRequest{
			IdempotencyKey: "scheduled-market-report:" + slotUTC.Format(time.RFC3339),
			ScheduledFor:   slotUTC, DataAsOf: snapshot.AsOf,
			Messages: messages, MaxAttempts: s.maxAttempts,
		})
		if err != nil {
			return result, err
		}
		result.Enqueued = true
		result.Created = enqueue.Created
	}
	for range 100 {
		found, err := s.dispatcher.DispatchOne(ctx, now)
		if err != nil {
			return result, err
		}
		if !found {
			break
		}
		result.Dispatched++
	}
	return result, nil
}

func DueSlot(now time.Time, hours []int, grace time.Duration) (time.Time, bool) {
	if !slices.Contains(hours, now.Hour()) {
		return time.Time{}, false
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
	if now.Before(slot) || !now.Before(slot.Add(grace)) {
		return time.Time{}, false
	}
	return slot, true
}
