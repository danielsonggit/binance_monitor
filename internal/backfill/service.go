package backfill

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"binance-monitor/internal/binancevision"
	"binance-monitor/internal/domain/market"
)

type Repository interface {
	ActiveSymbols(context.Context) ([]string, error)
	ExistingOpenTimes(context.Context, []string, time.Time, time.Time) (map[string][]time.Time, error)
	Save(context.Context, market.KlineBatch) (market.KlineWriteResult, error)
}

type ArchiveSource interface {
	FetchDailyKlines(context.Context, string, market.KlineInterval, time.Time) ([]market.Kline, error)
}

type RESTSource interface {
	FetchKlines(context.Context, market.KlineQuery) ([]market.Kline, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Failure struct {
	Symbol string
	Start  time.Time
	End    time.Time
	Err    error
}

type Result struct {
	WindowStart     time.Time
	WindowEnd       time.Time
	Concurrency     int
	Symbols         int
	Expected        int
	PresentBefore   int
	Written         int
	ArchiveDays     int
	RESTRequests    int
	Remaining       int
	RemainingRanges int
	PlannedGaps     []Gap
	RemainingGaps   []Gap
	Failures        []Failure
}

type Service struct {
	repository Repository
	archive    ArchiveSource
	rest       RESTSource
	clock      Clock
}

func NewService(repository Repository, archive ArchiveSource, rest RESTSource, clock Clock) (*Service, error) {
	if repository == nil || archive == nil || rest == nil || clock == nil {
		return nil, fmt.Errorf("backfill service 依赖不能为空")
	}
	return &Service{repository: repository, archive: archive, rest: rest, clock: clock}, nil
}

func (s *Service) Run(ctx context.Context, lookback time.Duration, concurrency int) (Result, error) {
	if concurrency <= 0 {
		return Result{}, fmt.Errorf("backfill concurrency 必须大于 0")
	}
	now := s.clock.Now().UTC()
	symbols, err := s.repository.ActiveSymbols(ctx)
	if err != nil {
		return Result{}, err
	}
	if len(symbols) == 0 {
		return Result{}, fmt.Errorf("没有 active instruments，必须先执行 worker 同步 Binance 合约目录")
	}
	empty, err := BuildPlan(now, lookback, market.KlineInterval15m, symbols, nil)
	if err != nil {
		return Result{}, err
	}
	existing, err := s.repository.ExistingOpenTimes(ctx, symbols, empty.WindowStart, empty.WindowEnd)
	if err != nil {
		return Result{}, err
	}
	plan, err := BuildPlan(now, lookback, market.KlineInterval15m, symbols, existing)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		WindowStart: plan.WindowStart, WindowEnd: plan.WindowEnd, Concurrency: concurrency,
		Symbols: len(symbols), Expected: plan.Expected, PresentBefore: plan.Present,
		PlannedGaps: append([]Gap(nil), plan.Gaps...),
	}
	if len(plan.Gaps) > 0 {
		outcomes, err := s.fillGaps(ctx, now, plan.Gaps, concurrency)
		if err != nil {
			return result, err
		}
		for _, outcome := range outcomes {
			result.Written += outcome.Written
			result.ArchiveDays += outcome.ArchiveDays
			result.RESTRequests += outcome.RESTRequests
			result.Failures = append(result.Failures, outcome.Failures...)
		}
	}
	finalExisting, err := s.repository.ExistingOpenTimes(ctx, symbols, plan.WindowStart, plan.WindowEnd)
	if err != nil {
		return result, err
	}
	finalPlan, err := BuildPlan(now, lookback, market.KlineInterval15m, symbols, finalExisting)
	if err != nil {
		return result, err
	}
	result.RemainingRanges = len(finalPlan.Gaps)
	result.RemainingGaps = append([]Gap(nil), finalPlan.Gaps...)
	for _, gap := range finalPlan.Gaps {
		result.Remaining += gap.Count
	}
	return result, nil
}

func (s *Service) fillGaps(
	ctx context.Context,
	now time.Time,
	gaps []Gap,
	concurrency int,
) ([]Result, error) {
	grouped := make(map[string][]Gap)
	order := make([]string, 0)
	for _, gap := range gaps {
		if _, exists := grouped[gap.Symbol]; !exists {
			order = append(order, gap.Symbol)
		}
		grouped[gap.Symbol] = append(grouped[gap.Symbol], gap)
	}
	if concurrency > len(order) {
		concurrency = len(order)
	}

	type outcome struct {
		symbol string
		result Result
	}
	jobs := make(chan string, len(order))
	results := make(chan outcome, len(order))
	for _, symbol := range order {
		jobs <- symbol
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			for symbol := range jobs {
				partial := Result{}
				archiveState := make(map[string]error)
				for _, gap := range grouped[symbol] {
					if ctx.Err() != nil {
						break
					}
					if err := s.fillGap(ctx, now, gap, archiveState, &partial); err != nil {
						partial.Failures = append(partial.Failures, Failure{
							Symbol: gap.Symbol, Start: gap.Start, End: gap.End, Err: err,
						})
					}
				}
				results <- outcome{symbol: symbol, result: partial}
			}
		}()
	}
	workers.Wait()
	close(results)

	bySymbol := make(map[string]Result, len(order))
	for item := range results {
		bySymbol[item.symbol] = item.result
	}
	ordered := make([]Result, 0, len(order))
	for _, symbol := range order {
		ordered = append(ordered, bySymbol[symbol])
	}
	if err := ctx.Err(); err != nil {
		return ordered, err
	}
	return ordered, nil
}

func (s *Service) fillGap(
	ctx context.Context,
	now time.Time,
	gap Gap,
	archiveState map[string]error,
	result *Result,
) error {
	today := now.Truncate(24 * time.Hour)
	for cursor := gap.Start; cursor.Before(gap.End); {
		day := cursor.Truncate(24 * time.Hour)
		segmentEnd := day.Add(24 * time.Hour)
		if segmentEnd.After(gap.End) {
			segmentEnd = gap.End
		}
		key := gap.Symbol + ":" + day.Format("2006-01-02")
		if day.Before(today) {
			state, attempted := archiveState[key]
			if attempted && state == nil {
				cursor = segmentEnd
				continue
			}
			if !attempted {
				items, err := s.archive.FetchDailyKlines(ctx, gap.Symbol, market.KlineInterval15m, day)
				archiveState[key] = err
				if err == nil {
					written, err := s.save(ctx, items, market.KlineSourceBinanceVision, now)
					if err != nil {
						return err
					}
					result.Written += written
					result.ArchiveDays++
					cursor = segmentEnd
					continue
				}
				if !errors.Is(err, binancevision.ErrArchiveNotFound) {
					return err
				}
			}
		}
		count := int(segmentEnd.Sub(cursor) / (15 * time.Minute))
		items, err := s.rest.FetchKlines(ctx, market.KlineQuery{
			Symbol: gap.Symbol, Interval: market.KlineInterval15m,
			StartTime: cursor, EndTime: segmentEnd.Add(-time.Millisecond), Limit: count,
		})
		result.RESTRequests++
		if err != nil {
			return err
		}
		filtered := make([]market.Kline, 0, len(items))
		for _, item := range items {
			if !item.OpenTime.Before(cursor) && item.OpenTime.Before(segmentEnd) {
				filtered = append(filtered, item)
			}
		}
		written, err := s.save(ctx, filtered, market.KlineSourceBinanceFutures, now)
		if err != nil {
			return err
		}
		result.Written += written
		cursor = segmentEnd
	}
	return nil
}

func (s *Service) save(ctx context.Context, items []market.Kline, source string, now time.Time) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	write, err := s.repository.Save(ctx, market.KlineBatch{Items: items, Source: source, ReceivedAt: now})
	if err != nil {
		return 0, err
	}
	return write.Upserted, nil
}
