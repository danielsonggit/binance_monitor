package collector

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/domain/market"
)

type KlineSource interface {
	FetchKlines(context.Context, market.KlineQuery) ([]market.Kline, error)
}

type KlineRepository interface {
	Save(context.Context, market.KlineBatch) (market.KlineWriteResult, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

type KlineCollectionResult struct {
	Fetched   int
	Completed int
	Dropped   int
	Upserted  int
	AsOf      time.Time
}

type KlineCollector struct {
	source     KlineSource
	repository KlineRepository
	clock      Clock
}

func NewKlineCollector(
	source KlineSource,
	repository KlineRepository,
	clock Clock,
) (*KlineCollector, error) {
	if source == nil || repository == nil || clock == nil {
		return nil, fmt.Errorf("K 线 collector 依赖不能为空")
	}
	return &KlineCollector{source: source, repository: repository, clock: clock}, nil
}

// Collect fetches one Binance page and persists only candles whose close time
// has passed. Full-universe pagination and backfill scheduling are deliberately
// kept outside this component so MHR-3 can reuse the same correctness boundary.
func (c *KlineCollector) Collect(
	ctx context.Context,
	query market.KlineQuery,
) (market.KlineWriteResult, KlineCollectionResult, error) {
	query = query.Normalized()
	if err := query.Validate(); err != nil {
		return market.KlineWriteResult{}, KlineCollectionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return market.KlineWriteResult{}, KlineCollectionResult{}, err
	}

	items, err := c.source.FetchKlines(ctx, query)
	if err != nil {
		return market.KlineWriteResult{}, KlineCollectionResult{}, fmt.Errorf(
			"采集 %s %s K 线: %w",
			query.Symbol,
			query.Interval,
			err,
		)
	}

	now := c.clock.Now().UTC()
	result := KlineCollectionResult{Fetched: len(items), AsOf: now}
	completed := make([]market.Kline, 0, len(items))
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return market.KlineWriteResult{}, result, fmt.Errorf("校验来源 K 线: %w", err)
		}
		if item.Symbol != query.Symbol || item.Interval != query.Interval {
			return market.KlineWriteResult{}, result, fmt.Errorf(
				"来源 K 线与请求不匹配：请求 %s/%s，收到 %s/%s",
				query.Symbol,
				query.Interval,
				item.Symbol,
				item.Interval,
			)
		}
		key := item.OpenTime.UnixMilli()
		if _, exists := seen[key]; exists {
			return market.KlineWriteResult{}, result, fmt.Errorf(
				"来源 K 线存在重复记录 %s/%d",
				item.Symbol,
				key,
			)
		}
		seen[key] = struct{}{}
		if item.IsClosed(now) {
			completed = append(completed, item)
		}
	}
	result.Completed = len(completed)
	result.Dropped = result.Fetched - result.Completed
	if len(completed) == 0 {
		return market.KlineWriteResult{}, result, nil
	}

	writeResult, err := c.repository.Save(ctx, market.KlineBatch{
		Items:      completed,
		Source:     market.KlineSourceBinanceFutures,
		ReceivedAt: now,
	})
	if err != nil {
		return market.KlineWriteResult{}, result, fmt.Errorf(
			"保存 %s %s K 线: %w",
			query.Symbol,
			query.Interval,
			err,
		)
	}
	if writeResult.Attempted != len(completed) {
		return market.KlineWriteResult{}, result, fmt.Errorf(
			"K 线 repository 返回无效 attempted=%d，期望 %d",
			writeResult.Attempted,
			len(completed),
		)
	}
	result.Upserted = writeResult.Upserted
	return writeResult, result, nil
}
