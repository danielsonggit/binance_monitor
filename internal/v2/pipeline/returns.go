package pipeline

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/backfill"
	"binance-monitor/internal/feature"
)

type HistoryBackfiller interface {
	Run(context.Context, time.Duration, int) (backfill.Result, error)
}

type FeatureCalculator interface {
	RunAt(context.Context, time.Time) (feature.Result, error)
}

type HistoryAuditor interface {
	Record(context.Context, backfill.Result, time.Time, time.Time) error
}

type ReturnPipeline struct {
	history          HistoryBackfiller
	auditor          HistoryAuditor
	features         FeatureCalculator
	backfillLookback time.Duration
	backfillWorkers  int
}

func NewReturnPipeline(
	history HistoryBackfiller,
	auditor HistoryAuditor,
	features FeatureCalculator,
	backfillLookback time.Duration,
	backfillWorkers int,
) (*ReturnPipeline, error) {
	if history == nil || auditor == nil || features == nil || backfillLookback < 24*time.Hour || backfillWorkers <= 0 {
		return nil, fmt.Errorf("return pipeline 配置或依赖无效")
	}
	return &ReturnPipeline{
		history: history, auditor: auditor, features: features,
		backfillLookback: backfillLookback, backfillWorkers: backfillWorkers,
	}, nil
}

func (p *ReturnPipeline) RunAt(ctx context.Context, asOf time.Time) (feature.Result, error) {
	startedAt := time.Now().UTC()
	history, err := p.history.Run(ctx, p.backfillLookback, p.backfillWorkers)
	if err != nil {
		return feature.Result{}, fmt.Errorf("收益计算前历史回补: %w", err)
	}
	if err := p.auditor.Record(ctx, history, startedAt, time.Now().UTC()); err != nil {
		return feature.Result{}, fmt.Errorf("记录收益计算前回补审计: %w", err)
	}
	// A newly listed or temporarily illiquid symbol can legitimately retain
	// gaps. The feature calculator records those horizons as invalid; it must
	// not prevent healthy symbols from being calculated.
	return p.features.RunAt(ctx, asOf)
}
