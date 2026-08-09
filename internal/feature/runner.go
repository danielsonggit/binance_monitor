package feature

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type CalculateAtService interface {
	RunAt(context.Context, time.Time) (Result, error)
}

type Runner struct {
	service  CalculateAtService
	interval time.Duration
	delay    time.Duration
	logger   *slog.Logger

	mu        sync.RWMutex
	ready     bool
	lastRun   time.Time
	lastError string
}

func NewRunner(
	service CalculateAtService,
	interval time.Duration,
	delay time.Duration,
	logger *slog.Logger,
) (*Runner, error) {
	if service == nil || logger == nil {
		return nil, fmt.Errorf("feature runner 依赖不能为空")
	}
	if interval <= 0 || time.Hour%interval != 0 || delay < 0 || delay >= interval {
		return nil, fmt.Errorf("feature runner interval/delay 无效")
	}
	return &Runner{service: service, interval: interval, delay: delay, logger: logger}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	r.calculate(ctx, time.Now().UTC().Truncate(r.interval))
	for {
		now := time.Now().UTC()
		asOf := now.Truncate(r.interval).Add(r.interval)
		wakeAt := asOf.Add(r.delay)
		timer := time.NewTimer(time.Until(wakeAt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			r.calculate(ctx, asOf)
		}
	}
}

// Calculate executes one aligned cycle and is exposed for deterministic tests
// and operational smoke checks without waiting for a wall-clock boundary.
func (r *Runner) Calculate(ctx context.Context, asOf time.Time) error {
	if asOf.IsZero() || !asOf.Equal(asOf.UTC().Truncate(r.interval)) {
		return fmt.Errorf("feature runner as_of 必须对齐 %s", r.interval)
	}
	return r.calculate(ctx, asOf.UTC())
}

func (r *Runner) calculate(ctx context.Context, asOf time.Time) error {
	result, err := r.service.RunAt(ctx, asOf)
	r.mu.Lock()
	r.ready = true
	if err != nil {
		r.lastError = err.Error()
	} else {
		r.lastRun = asOf
		r.lastError = ""
	}
	r.mu.Unlock()
	if err != nil {
		r.logger.Error("多周期收益率计算失败", "as_of", asOf, "error", err)
		return err
	}
	r.logger.Info(
		"多周期收益率计算完成",
		"as_of", result.AsOf,
		"symbols", result.Symbols,
		"valid_metrics", result.ValidMetrics,
		"invalid_metrics", result.InvalidMetrics,
		"written", result.Written,
	)
	return nil
}

func (r *Runner) Health() (bool, time.Time, string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ready && r.lastError == "", r.lastRun, r.lastError
}
