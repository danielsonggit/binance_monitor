package feature

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

const (
	InvalidCurrentMissing     = "CURRENT_PRICE_MISSING"
	InvalidCurrentStale       = "CURRENT_PRICE_STALE"
	InvalidCurrentLowQuality  = "CURRENT_PRICE_LOW_QUALITY"
	InvalidBaselineMissing    = "BASELINE_PRICE_MISSING"
	InvalidBaselineTooOld     = "BASELINE_PRICE_TOO_OLD"
	InvalidBaselineLowQuality = "BASELINE_PRICE_LOW_QUALITY"
	InvalidKlineGaps          = "KLINE_GAPS"
	InvalidNoRecentLiquidity  = "NO_RECENT_LIQUIDITY"
)

type Policy struct {
	CurrentMaxAge     time.Duration
	BaselineMaxOffset time.Duration
	MinimumQuality    int16
	LiquidityLookback time.Duration
	FeatureVersion    string
}

type Calculator struct {
	policy Policy
}

func NewCalculator(policy Policy) (*Calculator, error) {
	if policy.CurrentMaxAge <= 0 || policy.BaselineMaxOffset < 0 || policy.LiquidityLookback <= 0 {
		return nil, fmt.Errorf("feature 时间质量策略无效")
	}
	if policy.MinimumQuality < 0 || policy.MinimumQuality > 100 {
		return nil, fmt.Errorf("feature minimum quality 必须在 0 到 100 之间")
	}
	if strings.TrimSpace(policy.FeatureVersion) == "" {
		return nil, fmt.Errorf("feature version 不能为空")
	}
	return &Calculator{policy: policy}, nil
}

func (c *Calculator) Calculate(
	asOf time.Time,
	inputs []market.ReturnFeatureInput,
) ([]market.ReturnFeatureSet, error) {
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) {
		return nil, fmt.Errorf("feature as_of 必须按 %s UTC 对齐", market.SnapshotInterval)
	}
	ordered := append([]market.ReturnFeatureInput(nil), inputs...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Symbol < ordered[j].Symbol })
	result := make([]market.ReturnFeatureSet, 0, len(ordered))
	previous := ""
	for _, input := range ordered {
		if err := validateInput(input); err != nil {
			return nil, err
		}
		if input.Symbol == previous {
			return nil, fmt.Errorf("feature input 重复 symbol %s", input.Symbol)
		}
		previous = input.Symbol
		result = append(result, c.calculateOne(asOf, input))
	}
	return result, nil
}

func (c *Calculator) calculateOne(asOf time.Time, input market.ReturnFeatureInput) market.ReturnFeatureSet {
	prices := normalizePrices(input.Prices)
	current, hasCurrent := latestAtOrBefore(prices, asOf)
	currentAge := int64(0)
	if hasCurrent {
		currentAge = int64(asOf.Sub(current.ObservedAt) / time.Second)
	}
	recentVolume := sumQuoteVolume(input.Klines, asOf.Add(-c.policy.LiquidityLookback), asOf)
	volume24h := sumQuoteVolume(input.Klines, asOf.Add(-24*time.Hour), asOf)
	set := market.ReturnFeatureSet{
		Symbol: input.Symbol, Sector: input.Sector, AsOf: asOf,
		FeatureVersion:      c.policy.FeatureVersion,
		CurrentAgeSeconds:   currentAge,
		RecentQuoteVolume1h: recentVolume,
		QuoteVolume24h:      volume24h,
		Metrics:             make(map[market.ReturnHorizon]market.ReturnMetric, len(market.ReturnHorizons())),
	}
	if hasCurrent {
		set.CurrentPrice = current.Price
		set.CurrentPriceAt = current.ObservedAt
		set.CurrentSource = current.Source
	}
	for _, horizon := range market.ReturnHorizons() {
		duration, _ := horizon.Duration()
		target := asOf.Add(-duration)
		baseline, hasBaseline := latestAtOrBefore(prices, target)
		metric := market.ReturnMetric{
			Horizon:  horizon,
			TargetAt: target,
			GapCount: countKlineGaps(input.Klines, target, asOf),
		}
		if hasBaseline {
			metric.BaselinePrice = baseline.Price
			metric.BaselinePriceAt = baseline.ObservedAt
			metric.BaselineSource = baseline.Source
			metric.BaselineOffsetSeconds = int64(target.Sub(baseline.ObservedAt) / time.Second)
		}
		metric.InvalidReason = c.invalidReason(
			hasCurrent, current, currentAge,
			hasBaseline, baseline, metric.BaselineOffsetSeconds,
			metric.GapCount, recentVolume,
		)
		if metric.InvalidReason == "" {
			metric.IsValid = true
			metric.ReturnPercent = current.Price.Div(baseline.Price).Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100))
		}
		set.Metrics[horizon] = metric
	}
	return set
}

func (c *Calculator) invalidReason(
	hasCurrent bool,
	current market.FeaturePricePoint,
	currentAge int64,
	hasBaseline bool,
	baseline market.FeaturePricePoint,
	baselineOffset int64,
	gaps int,
	recentVolume decimal.Decimal,
) string {
	switch {
	case !hasCurrent:
		return InvalidCurrentMissing
	case time.Duration(currentAge)*time.Second > c.policy.CurrentMaxAge:
		return InvalidCurrentStale
	case current.QualityScore < c.policy.MinimumQuality:
		return InvalidCurrentLowQuality
	case !hasBaseline:
		return InvalidBaselineMissing
	case time.Duration(baselineOffset)*time.Second > c.policy.BaselineMaxOffset:
		return InvalidBaselineTooOld
	case baseline.QualityScore < c.policy.MinimumQuality:
		return InvalidBaselineLowQuality
	case gaps > 0:
		return InvalidKlineGaps
	case !recentVolume.IsPositive():
		return InvalidNoRecentLiquidity
	default:
		return ""
	}
}

func validateInput(input market.ReturnFeatureInput) error {
	if strings.TrimSpace(input.Symbol) == "" || input.Symbol != strings.ToUpper(strings.TrimSpace(input.Symbol)) {
		return fmt.Errorf("feature input symbol 必须是规范化的大写值")
	}
	if input.Sector != market.SectorCrypto && input.Sector != market.SectorTradFi {
		return fmt.Errorf("feature input %s sector 无效", input.Symbol)
	}
	for _, point := range input.Prices {
		if point.ObservedAt.IsZero() || !point.Price.IsPositive() || strings.TrimSpace(point.Source) == "" ||
			point.QualityScore < 0 || point.QualityScore > 100 {
			return fmt.Errorf("feature input %s 包含无效价格点", input.Symbol)
		}
		switch point.Source {
		case market.PriceSourceKline15m:
			if !point.ObservedAt.Equal(point.ObservedAt.UTC().Truncate(15 * time.Minute)) {
				return fmt.Errorf("feature input %s K 线价格点未对齐", input.Symbol)
			}
		case market.PriceSourceSnapshot5m:
			if !point.ObservedAt.Equal(point.ObservedAt.UTC().Truncate(market.SnapshotInterval)) {
				return fmt.Errorf("feature input %s snapshot 价格点未对齐", input.Symbol)
			}
		default:
			return fmt.Errorf("feature input %s 价格 source 无效", input.Symbol)
		}
	}
	for _, point := range input.Klines {
		if point.CloseAt.IsZero() || !point.CloseAt.Equal(point.CloseAt.UTC().Truncate(15*time.Minute)) ||
			point.QuoteVolume.IsNegative() || point.TradeCount < 0 {
			return fmt.Errorf("feature input %s 包含无效 K 线点", input.Symbol)
		}
	}
	return nil
}

func normalizePrices(points []market.FeaturePricePoint) []market.FeaturePricePoint {
	result := append([]market.FeaturePricePoint(nil), points...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].ObservedAt.Equal(result[j].ObservedAt) {
			return priceSourcePriority(result[i].Source) < priceSourcePriority(result[j].Source)
		}
		return result[i].ObservedAt.Before(result[j].ObservedAt)
	})
	deduplicated := result[:0]
	for _, point := range result {
		if len(deduplicated) > 0 && deduplicated[len(deduplicated)-1].ObservedAt.Equal(point.ObservedAt) {
			if priceSourcePriority(point.Source) > priceSourcePriority(deduplicated[len(deduplicated)-1].Source) {
				deduplicated[len(deduplicated)-1] = point
			}
			continue
		}
		deduplicated = append(deduplicated, point)
	}
	return deduplicated
}

func priceSourcePriority(source string) int {
	if source == market.PriceSourceKline15m {
		return 2
	}
	if source == market.PriceSourceSnapshot5m {
		return 1
	}
	return 0
}

func latestAtOrBefore(points []market.FeaturePricePoint, target time.Time) (market.FeaturePricePoint, bool) {
	index := sort.Search(len(points), func(i int) bool { return points[i].ObservedAt.After(target) })
	if index == 0 {
		return market.FeaturePricePoint{}, false
	}
	return points[index-1], true
}

func countKlineGaps(points []market.FeatureKlinePoint, start, end time.Time) int {
	present := make(map[int64]struct{}, len(points))
	for _, point := range points {
		if point.CloseAt.After(start) && !point.CloseAt.After(end) {
			present[point.CloseAt.UnixMilli()] = struct{}{}
		}
	}
	first := start.UTC().Truncate(15 * time.Minute)
	if !first.After(start) {
		first = first.Add(15 * time.Minute)
	}
	gaps := 0
	for closeAt := first; !closeAt.After(end); closeAt = closeAt.Add(15 * time.Minute) {
		if _, exists := present[closeAt.UnixMilli()]; !exists {
			gaps++
		}
	}
	return gaps
}

func sumQuoteVolume(points []market.FeatureKlinePoint, start, end time.Time) decimal.Decimal {
	total := decimal.Zero
	for _, point := range points {
		if point.CloseAt.After(start) && !point.CloseAt.After(end) {
			total = total.Add(point.QuoteVolume)
		}
	}
	return total
}
