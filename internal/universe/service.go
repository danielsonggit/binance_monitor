package universe

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/domain/market"
)

type Source interface {
	FetchInstruments(context.Context, []string) ([]market.Instrument, error)
}

type Repository interface {
	ActiveCount(context.Context) (int, error)
	Reconcile(context.Context, Snapshot) (Result, error)
}

type Clock interface {
	Now() time.Time
}

type Snapshot struct {
	ObservedAt           time.Time
	Instruments          []market.Instrument
	MissingConfirmations int
}

type Result struct {
	Observed       int
	ActiveBefore   int
	Inserted       int
	Updated        int
	MissingPending int
	Closed         int
	AlreadyApplied bool
}

type Service struct {
	source               Source
	repository           Repository
	clock                Clock
	quoteAssets          []string
	minimumRatioPercent  int
	missingConfirmations int
}

func New(
	source Source,
	repository Repository,
	clock Clock,
	quoteAssets []string,
	minimumRatioPercent int,
	missingConfirmations int,
) *Service {
	return &Service{
		source:               source,
		repository:           repository,
		clock:                clock,
		quoteAssets:          append([]string(nil), quoteAssets...),
		minimumRatioPercent:  minimumRatioPercent,
		missingConfirmations: missingConfirmations,
	}
}

func (s *Service) Sync(ctx context.Context) (Result, error) {
	instruments, err := s.source.FetchInstruments(ctx, s.quoteAssets)
	if err != nil {
		return Result{}, fmt.Errorf("读取 Binance 合约目录: %w", err)
	}
	if err := validateSnapshot(instruments); err != nil {
		return Result{}, err
	}

	activeCount, err := s.repository.ActiveCount(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("读取当前合约数量: %w", err)
	}
	if activeCount > 0 && len(instruments)*100 < activeCount*s.minimumRatioPercent {
		return Result{}, fmt.Errorf(
			"拒绝异常合约目录：本次 %d，当前 %d，低于最低比例 %d%%",
			len(instruments),
			activeCount,
			s.minimumRatioPercent,
		)
	}

	result, err := s.repository.Reconcile(ctx, Snapshot{
		ObservedAt:           s.clock.Now().UTC().Truncate(time.Minute),
		Instruments:          instruments,
		MissingConfirmations: s.missingConfirmations,
	})
	if err != nil {
		return Result{}, fmt.Errorf("保存合约目录: %w", err)
	}
	return result, nil
}

func validateSnapshot(instruments []market.Instrument) error {
	if len(instruments) == 0 {
		return fmt.Errorf("拒绝空合约目录")
	}
	seen := make(map[string]struct{}, len(instruments))
	for _, instrument := range instruments {
		if err := instrument.Validate(); err != nil {
			return err
		}
		if _, exists := seen[instrument.Symbol]; exists {
			return fmt.Errorf("合约目录包含重复 symbol：%s", instrument.Symbol)
		}
		seen[instrument.Symbol] = struct{}{}
	}
	return nil
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }
