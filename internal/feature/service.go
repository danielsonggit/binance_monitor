package feature

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/domain/market"
)

type Repository interface {
	LoadReturnInputs(context.Context, time.Time, time.Duration) ([]market.ReturnFeatureInput, error)
	SaveReturnFeatures(context.Context, market.ReturnFeatureBatch) (market.ReturnFeatureWriteResult, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Result struct {
	AsOf           time.Time
	Symbols        int
	ValidMetrics   int
	InvalidMetrics int
	InvalidReasons map[string]int
	Written        int
}

type Service struct {
	repository Repository
	calculator *Calculator
	clock      Clock
	lookback   time.Duration
}

func NewService(
	repository Repository,
	calculator *Calculator,
	clock Clock,
	lookback time.Duration,
) (*Service, error) {
	if repository == nil || calculator == nil || clock == nil {
		return nil, fmt.Errorf("feature service 依赖不能为空")
	}
	if lookback < 24*time.Hour {
		return nil, fmt.Errorf("feature lookback 不能小于 24 小时")
	}
	return &Service{repository: repository, calculator: calculator, clock: clock, lookback: lookback}, nil
}

func (s *Service) Run(ctx context.Context) (Result, error) {
	return s.RunAt(ctx, s.clock.Now().UTC().Truncate(market.SnapshotInterval))
}

func (s *Service) RunAt(ctx context.Context, asOf time.Time) (Result, error) {
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) {
		return Result{}, fmt.Errorf("feature as_of 必须按 %s UTC 对齐", market.SnapshotInterval)
	}
	inputs, err := s.repository.LoadReturnInputs(ctx, asOf, s.lookback)
	if err != nil {
		return Result{}, err
	}
	if len(inputs) == 0 {
		return Result{}, fmt.Errorf("没有 active instruments，无法计算收益特征")
	}
	items, err := s.calculator.Calculate(asOf, inputs)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		AsOf: asOf, Symbols: len(items), InvalidReasons: make(map[string]int),
	}
	for _, item := range items {
		for _, horizon := range market.ReturnHorizons() {
			metric := item.Metrics[horizon]
			if metric.IsValid {
				result.ValidMetrics++
			} else {
				result.InvalidMetrics++
				result.InvalidReasons[metric.InvalidReason]++
			}
		}
	}
	write, err := s.repository.SaveReturnFeatures(ctx, market.ReturnFeatureBatch{
		AsOf: asOf, FeatureVersion: s.calculator.policy.FeatureVersion,
		CalculatedAt: s.clock.Now().UTC(), Items: items,
	})
	if err != nil {
		return result, err
	}
	result.Written = write.Upserted
	return result, nil
}
