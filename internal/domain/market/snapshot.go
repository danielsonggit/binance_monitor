package market

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

const (
	SnapshotJobType  = "MARKET_SNAPSHOT_5M"
	SnapshotInterval = 5 * time.Minute
)

type PricePoint struct {
	Symbol          string
	ObservedAt      time.Time
	Price           decimal.Decimal
	SourceEventTime time.Time
	ReceivedAt      time.Time
	QualityScore    int16
}

type SnapshotItem struct {
	Ticker       MiniTicker
	QualityScore int16
}

type SnapshotBatch struct {
	BucketStart time.Time
	BucketEnd   time.Time
	Items       []SnapshotItem
}

func (b SnapshotBatch) Validate(interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("snapshot interval 必须大于 0")
	}
	if b.BucketStart.IsZero() || b.BucketEnd.IsZero() {
		return fmt.Errorf("snapshot bucket 时间不能为空")
	}
	if !b.BucketEnd.Equal(b.BucketStart.Add(interval)) {
		return fmt.Errorf("snapshot bucket 必须恰好为 %s", interval)
	}
	if !b.BucketStart.Equal(b.BucketStart.Truncate(interval)) {
		return fmt.Errorf("snapshot bucket_start 必须对齐 %s", interval)
	}
	seen := make(map[string]struct{}, len(b.Items))
	for _, item := range b.Items {
		if err := item.Ticker.Validate(); err != nil {
			return err
		}
		if _, exists := seen[item.Ticker.Symbol]; exists {
			return fmt.Errorf("snapshot 中存在重复 symbol %s", item.Ticker.Symbol)
		}
		seen[item.Ticker.Symbol] = struct{}{}
		if item.QualityScore < 0 || item.QualityScore > 100 {
			return fmt.Errorf("%s quality score 必须在 0 到 100 之间", item.Ticker.Symbol)
		}
	}
	return nil
}

type SnapshotWriteResult struct {
	Expected       int
	Actual         int
	Missing        int
	MissingSymbols []string
	Status         string
	AlreadyApplied bool
}
