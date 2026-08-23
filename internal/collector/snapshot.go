package collector

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"binance-monitor/internal/domain/market"
)

const maxBoundarySkew = 2 * time.Second

type LatestReader interface {
	Snapshot() map[string]market.MiniTicker
}

type SourceHealthReader interface {
	Health() (bool, time.Time, string)
}

type WindowWriter interface {
	Apply([]market.PricePoint)
	Prune(time.Time)
}

type SnapshotRepository interface {
	Save(context.Context, market.SnapshotBatch) (market.SnapshotWriteResult, error)
	LoadRecent(context.Context, time.Time) ([]market.PricePoint, error)
	MarkGaps(context.Context, time.Time) (int, error)
}

type SnapshotCollector struct {
	latest                 LatestReader
	source                 SourceHealthReader
	windows                WindowWriter
	repository             SnapshotRepository
	retention              time.Duration
	maxEventAge            time.Duration
	interval               time.Duration
	minimumCoveragePercent int
	logger                 *slog.Logger

	mu           sync.RWMutex
	ready        bool
	lastPersist  time.Time
	lastError    string
	lastExpected int
	lastActual   int
	lastCoverage market.SnapshotCoverage
}

func NewSnapshotCollector(
	latest LatestReader,
	source SourceHealthReader,
	windows WindowWriter,
	repository SnapshotRepository,
	retention time.Duration,
	maxEventAge time.Duration,
	interval time.Duration,
	minimumCoveragePercent int,
	logger *slog.Logger,
) (*SnapshotCollector, error) {
	if latest == nil || source == nil || windows == nil || repository == nil {
		return nil, fmt.Errorf("snapshot collector 依赖不能为空")
	}
	if retention < interval || maxEventAge <= 0 || interval <= 0 ||
		minimumCoveragePercent <= 0 || minimumCoveragePercent > 100 {
		return nil, fmt.Errorf("snapshot collector 时间配置无效")
	}
	if time.Hour%interval != 0 || interval%time.Minute != 0 {
		return nil, fmt.Errorf("snapshot interval 必须是能够整除一小时的整分钟")
	}
	return &SnapshotCollector{
		latest:                 latest,
		source:                 source,
		windows:                windows,
		repository:             repository,
		retention:              retention,
		maxEventAge:            maxEventAge,
		interval:               interval,
		minimumCoveragePercent: minimumCoveragePercent,
		logger:                 logger,
	}, nil
}

func (c *SnapshotCollector) Run(ctx context.Context) error {
	c.initialize(ctx, time.Now().UTC())
	for {
		now := time.Now().UTC()
		boundary := now.Truncate(time.Minute).Add(time.Minute)
		timer := time.NewTimer(time.Until(boundary))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			if err := c.Collect(ctx, boundary); err != nil {
				c.logger.Error("分钟行情采样失败", "boundary", boundary, "error", err)
			}
		}
	}
}

// Collect samples the latest materialized view at a closed minute boundary.
// It is exported so boundary and freshness behavior can be tested without
// sleeping or connecting to Binance.
func (c *SnapshotCollector) Collect(ctx context.Context, boundary time.Time) error {
	boundary = boundary.UTC().Truncate(time.Minute)
	latest := c.latest.Snapshot()
	symbols := make([]string, 0, len(latest))
	for symbol := range latest {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)

	points := make([]market.PricePoint, 0, len(symbols))
	items := make([]market.SnapshotItem, 0, len(symbols))
	observedSymbols := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		ticker := latest[symbol]
		if err := ticker.Validate(); err != nil {
			c.logger.Warn("忽略无效 mini ticker", "symbol", symbol, "error", err)
			continue
		}
		observedSymbols = append(observedSymbols, symbol)
		age := boundary.Sub(ticker.EventTime)
		if age < -maxBoundarySkew || age > c.maxEventAge {
			continue
		}
		if age < 0 {
			age = 0
		}
		quality := qualityScore(age, c.maxEventAge)
		points = append(points, market.PricePoint{
			Symbol:          ticker.Symbol,
			ObservedAt:      boundary,
			Price:           ticker.LastPrice,
			SourceEventTime: ticker.EventTime,
			ReceivedAt:      ticker.ReceivedAt,
			QualityScore:    quality,
		})
		items = append(items, market.SnapshotItem{Ticker: ticker, QualityScore: quality})
	}
	c.windows.Apply(points)
	c.windows.Prune(boundary.Add(-c.retention))
	if !boundary.Equal(boundary.Truncate(c.interval)) {
		return nil
	}

	bucketStart := boundary.Add(-c.interval)
	gaps, err := c.repository.MarkGaps(ctx, bucketStart)
	if err != nil {
		c.setFailure(err)
		return fmt.Errorf("标记历史 snapshot 缺口: %w", err)
	}
	if gaps > 0 {
		c.logger.Warn("登记了未采集的历史 snapshot 窗口", "buckets", gaps)
	}
	result, err := c.repository.Save(ctx, market.SnapshotBatch{
		BucketStart:     bucketStart,
		BucketEnd:       boundary,
		Items:           items,
		ObservedSymbols: observedSymbols,
		SourceAvailable: sourceAvailable(c.source),
	})
	if err != nil {
		c.setFailure(err)
		return err
	}
	c.setResult(boundary, result)
	if result.Missing > 0 {
		c.logger.Warn(
			"5 分钟全市场快照存在缺口",
			"bucket_start", bucketStart,
			"expected", result.Expected,
			"actual", result.Actual,
			"missing", result.Missing,
			"adjusted_expected", result.Coverage.AdjustedExpected,
			"adjusted_actual", result.Coverage.AdjustedActual,
			"availability_rule", result.Coverage.RuleVersion,
		)
	}
	return nil
}

func (c *SnapshotCollector) Health() (bool, time.Time, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.ready || c.lastError != "" {
		return false, c.lastPersist, c.lastError
	}
	if c.lastPersist.IsZero() {
		return false, c.lastPersist, "尚未完成 5 分钟快照"
	}
	if c.lastCoverage.RuleVersion != "" {
		if c.lastCoverage.Healthy(c.minimumCoveragePercent) {
			return true, c.lastPersist, ""
		}
		return false, c.lastPersist, fmt.Sprintf(
			"5 分钟会话调整覆盖不足 %d/%d，最低要求 %d%%（规则 %s）",
			c.lastCoverage.AdjustedActual, c.lastCoverage.AdjustedExpected,
			c.minimumCoveragePercent, c.lastCoverage.RuleVersion,
		)
	}
	if c.lastExpected <= 0 {
		return false, c.lastPersist, "尚未完成 5 分钟快照"
	}
	if c.lastActual*100 < c.lastExpected*c.minimumCoveragePercent {
		return false, c.lastPersist, fmt.Sprintf(
			"5 分钟快照覆盖不足 %d/%d，最低要求 %d%%",
			c.lastActual, c.lastExpected, c.minimumCoveragePercent,
		)
	}
	return true, c.lastPersist, ""
}

func (c *SnapshotCollector) Coverage() market.SnapshotCoverage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastCoverage
}

func (c *SnapshotCollector) initialize(ctx context.Context, now time.Time) {
	completedBucketStart := now.UTC().Truncate(c.interval)
	if gaps, err := c.repository.MarkGaps(ctx, completedBucketStart); err != nil {
		c.setFailure(fmt.Errorf("启动缺口检查: %w", err))
		c.logger.Error("启动 snapshot 缺口检查失败", "error", err)
	} else if gaps > 0 {
		c.setFailure(fmt.Errorf("检测到 %d 个未采集的 5 分钟窗口", gaps))
		c.logger.Warn("检测到进程停机期间的 snapshot 缺口", "buckets", gaps)
	}
	points, err := c.repository.LoadRecent(ctx, now.Add(-c.retention))
	if err != nil {
		c.setFailure(fmt.Errorf("预热分钟窗口: %w", err))
		c.logger.Error("从 PostgreSQL 预热分钟窗口失败", "error", err)
		return
	}
	c.windows.Apply(points)
	c.mu.Lock()
	c.ready = true
	c.mu.Unlock()
	c.logger.Info("分钟行情窗口预热完成", "points", len(points))
}

func (c *SnapshotCollector) setFailure(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready = true
	c.lastError = err.Error()
}

func (c *SnapshotCollector) setResult(observedAt time.Time, result market.SnapshotWriteResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready = true
	c.lastPersist = observedAt
	c.lastExpected = result.Expected
	c.lastActual = result.Actual
	c.lastCoverage = result.Coverage
	c.lastError = ""
}

func sourceAvailable(source SourceHealthReader) bool {
	healthy, _, _ := source.Health()
	return healthy
}

func qualityScore(age, maxAge time.Duration) int16 {
	switch {
	case age <= 15*time.Second:
		return 100
	case age <= 30*time.Second:
		return 90
	case age <= time.Minute:
		return 75
	case age <= maxAge:
		return 50
	default:
		return 0
	}
}
