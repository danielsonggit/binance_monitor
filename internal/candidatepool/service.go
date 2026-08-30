package candidatepool

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/domain/signal"
)

const JobType = "CANDIDATE_POOL_5M"

type Repository interface {
	LoadCandidateResult(context.Context, time.Time, string) (signal.CandidateWriteResult, bool, error)
	LoadCandidateInputs(context.Context, time.Time, string) ([]signal.CandidateInput, error)
	LoadCandidateMembers(context.Context, string) ([]signal.CandidateMember, error)
	SaveCandidateBatch(context.Context, signal.CandidateBatch) (signal.CandidateWriteResult, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	repository Repository
	calculator *Calculator
	clock      Clock
}

func NewService(repository Repository, calculator *Calculator, clock Clock) (*Service, error) {
	if repository == nil || calculator == nil || clock == nil {
		return nil, fmt.Errorf("candidate pool 依赖不能为空")
	}
	return &Service{repository: repository, calculator: calculator, clock: clock}, nil
}

func (s *Service) RunAt(ctx context.Context, asOf time.Time) (signal.CandidateWriteResult, error) {
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) {
		return signal.CandidateWriteResult{}, fmt.Errorf("candidate pool as_of 必须按 %s UTC 对齐", market.SnapshotInterval)
	}
	if existing, found, err := s.repository.LoadCandidateResult(ctx, asOf, s.calculator.rules.RuleVersion); err != nil {
		return signal.CandidateWriteResult{}, err
	} else if found {
		existing.AlreadyApplied = true
		return existing, nil
	}
	inputs, err := s.repository.LoadCandidateInputs(ctx, asOf, s.calculator.rules.FeatureVersion)
	if err != nil {
		return signal.CandidateWriteResult{}, err
	}
	if len(inputs) == 0 {
		return signal.CandidateWriteResult{}, fmt.Errorf("candidate pool %s 没有输入", asOf.Format(time.RFC3339))
	}
	members, err := s.repository.LoadCandidateMembers(ctx, s.calculator.rules.RuleVersion)
	if err != nil {
		return signal.CandidateWriteResult{}, err
	}
	batch, err := s.calculator.Calculate(asOf, s.clock.Now().UTC(), inputs, members)
	if err != nil {
		return signal.CandidateWriteResult{}, err
	}
	return s.repository.SaveCandidateBatch(ctx, batch)
}
