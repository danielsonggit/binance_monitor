package ranking

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/domain/market"
)

type Repository interface {
	LoadRankingInputs(context.Context, time.Time, string) ([]market.RankingInput, error)
	SaveRankings(context.Context, market.RankingBatch) (market.RankingWriteResult, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Result struct {
	AsOf          time.Time
	Groups        int
	Items         int
	ActiveMetrics int
	Eligible      int
	Positive      int
	Written       int
}

type Service struct {
	repository Repository
	calculator *Calculator
	clock      Clock
}

func NewService(repository Repository, calculator *Calculator, clock Clock) (*Service, error) {
	if repository == nil || calculator == nil || clock == nil {
		return nil, fmt.Errorf("ranking service 依赖不能为空")
	}
	return &Service{repository: repository, calculator: calculator, clock: clock}, nil
}

func (s *Service) Run(ctx context.Context) (Result, error) {
	return s.RunAt(ctx, s.clock.Now().UTC().Truncate(market.SnapshotInterval))
}

func (s *Service) RunAt(ctx context.Context, asOf time.Time) (Result, error) {
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) {
		return Result{}, fmt.Errorf("ranking as_of 必须按 %s UTC 对齐", market.SnapshotInterval)
	}
	inputs, err := s.repository.LoadRankingInputs(ctx, asOf, s.calculator.policy.FeatureVersion)
	if err != nil {
		return Result{}, err
	}
	groups, err := s.calculator.Calculate(asOf, inputs)
	if err != nil {
		return Result{}, err
	}
	result := Result{AsOf: asOf, Groups: len(groups)}
	for _, group := range groups {
		result.Items += len(group.Items)
		result.ActiveMetrics += group.ActiveCount
		result.Eligible += group.EligibleCount
		result.Positive += group.PositiveCount
	}
	write, err := s.repository.SaveRankings(ctx, market.RankingBatch{
		AsOf: asOf, RankingVersion: s.calculator.policy.RankingVersion,
		FeatureVersion: s.calculator.policy.FeatureVersion,
		CalculatedAt:   s.clock.Now().UTC(), Groups: groups,
	})
	if err != nil {
		return result, err
	}
	result.Written = write.ItemsWritten
	return result, nil
}
