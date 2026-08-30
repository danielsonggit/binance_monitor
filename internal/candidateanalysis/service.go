package candidateanalysis

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"binance-monitor/internal/domain/market"
)

const klineWarmup = 6 * time.Hour

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	source Source
	clock  Clock
}

func NewService(source Source, clock Clock) (*Service, error) {
	if source == nil || clock == nil {
		return nil, fmt.Errorf("candidate analysis 依赖不能为空")
	}
	return &Service{source: source, clock: clock}, nil
}

func (s *Service) Run(ctx context.Context, options Options) (Report, error) {
	if options.Lookback < time.Hour || options.Lookback > 14*24*time.Hour {
		return Report{}, fmt.Errorf("candidate analysis lookback 必须在 1 小时到 14 天之间")
	}
	if options.FeatureVersion == "" {
		return Report{}, fmt.Errorf("candidate analysis feature version 不能为空")
	}
	latest, err := s.source.LatestFeatureAsOf(ctx, options.FeatureVersion)
	if err != nil {
		return Report{}, err
	}
	end := options.End.UTC()
	if end.IsZero() {
		end = latest
	} else if end.After(latest) {
		return Report{}, fmt.Errorf("candidate analysis end %s 晚于最新已落库时点 %s", end.Format(time.RFC3339), latest.Format(time.RFC3339))
	}
	if end.IsZero() || !end.Equal(end.Truncate(market.SnapshotInterval)) {
		return Report{}, fmt.Errorf("candidate analysis end 必须是已落库的 UTC 五分钟时点")
	}
	start := end.Add(-options.Lookback)
	features, err := s.source.FeatureObservations(ctx, start, end, options.FeatureVersion)
	if err != nil {
		return Report{}, err
	}
	if len(features) == 0 {
		return Report{}, fmt.Errorf("candidate analysis 在 %s 到 %s 没有有效 feature 数据", start.Format(time.RFC3339), end.Format(time.RFC3339))
	}
	endPresent := false
	for _, observation := range features {
		if observation.AsOf.Equal(end) {
			endPresent = true
			break
		}
	}
	if !endPresent {
		return Report{}, fmt.Errorf("candidate analysis end %s 没有有效 feature 窗口", end.Format(time.RFC3339))
	}
	klines, err := s.source.KlineObservations(ctx, start.Add(-klineWarmup), end)
	if err != nil {
		return Report{}, err
	}
	report, err := analyze(start, end, options.FeatureVersion, s.clock.Now().UTC(), features, klines)
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

type sectorAccumulator struct {
	featureRows        int
	windows            int
	returns15          []float64
	returns1h          []float64
	acceleration       []float64
	volume1h           []float64
	volume24h          []float64
	breadth15          []float64
	breadth1h          []float64
	threshold15        []float64
	threshold1h        []float64
	candidateCounts    []float64
	turnovers          []float64
	unique             map[string]struct{}
	entries            int
	exits              int
	comparisons        int
	previousCandidates map[string]struct{}
	previousAsOf       time.Time
	klineRows          int
	volumeExpansion    []float64
	drawdown1h         []float64
	positiveStreak     []float64
}

func analyze(start, end time.Time, featureVersion string, generatedAt time.Time, features []FeatureObservation, klines []KlineObservation) (Report, error) {
	accumulators := map[market.Sector]*sectorAccumulator{
		market.SectorCrypto: newSectorAccumulator(),
		market.SectorTradFi: newSectorAccumulator(),
	}
	sort.Slice(features, func(i, j int) bool {
		if features[i].AsOf.Equal(features[j].AsOf) {
			if features[i].Sector == features[j].Sector {
				return features[i].Symbol < features[j].Symbol
			}
			return features[i].Sector < features[j].Sector
		}
		return features[i].AsOf.Before(features[j].AsOf)
	})
	for startIndex := 0; startIndex < len(features); {
		endIndex := startIndex + 1
		for endIndex < len(features) && features[endIndex].AsOf.Equal(features[startIndex].AsOf) && features[endIndex].Sector == features[startIndex].Sector {
			endIndex++
		}
		sector := features[startIndex].Sector
		accumulator, exists := accumulators[sector]
		if !exists {
			return Report{}, fmt.Errorf("candidate analysis sector 无效: %s", sector)
		}
		accumulateFeatureWindow(accumulator, sector, features[startIndex:endIndex])
		startIndex = endIndex
	}
	accumulateKlines(accumulators, start, end, klines)

	report := Report{
		AnalysisVersion: AnalysisVersion1,
		FeatureVersion:  featureVersion,
		Start:           start.UTC(),
		End:             end.UTC(),
		GeneratedAt:     generatedAt.UTC(),
		Notes: []string{
			"候选模拟只应用板块横截面分位数和绝对收益门槛，尚未应用待校准的最低流动性门槛。",
			"换手率定义为相邻有效五分钟窗口候选集合的对称差除以并集。",
			"量能倍数使用当前闭合 15 分钟 K 线成交额除以前 20 根闭合 K 线成交额中位数。",
			"所有结果均为只读研究统计，不是投资信号，也不会写数据库或发送 Telegram。",
		},
	}
	for _, sector := range []market.Sector{market.SectorCrypto, market.SectorTradFi} {
		report.Sectors = append(report.Sectors, buildSectorReport(sector, accumulators[sector]))
	}
	return report, nil
}

func newSectorAccumulator() *sectorAccumulator {
	return &sectorAccumulator{unique: make(map[string]struct{})}
}

func candidateFloors(sector market.Sector) (float64, float64) {
	if sector == market.SectorTradFi {
		return 0.5, 1.0
	}
	return 1.5, 3.0
}

func accumulateFeatureWindow(acc *sectorAccumulator, sector market.Sector, rows []FeatureObservation) {
	returns15 := make([]float64, 0, len(rows))
	returns1h := make([]float64, 0, len(rows))
	positive15 := 0
	positive1h := 0
	for _, row := range rows {
		if !allFinite(row.Return15m, row.Return1h, row.RecentQuoteVolume1h, row.QuoteVolume24h) {
			continue
		}
		acc.featureRows++
		acc.returns15 = append(acc.returns15, row.Return15m)
		acc.returns1h = append(acc.returns1h, row.Return1h)
		acc.volume1h = append(acc.volume1h, row.RecentQuoteVolume1h)
		acc.volume24h = append(acc.volume24h, row.QuoteVolume24h)
		returns15 = append(returns15, row.Return15m)
		returns1h = append(returns1h, row.Return1h)
		if row.Return15m > 0 {
			positive15++
		}
		if row.Return1h > 0 {
			positive1h++
		}
		if row.PreviousReturn15m != nil && row.Previous15mAsOf.Equal(row.AsOf.Add(-15*time.Minute)) && allFinite(*row.PreviousReturn15m) {
			acc.acceleration = append(acc.acceleration, row.Return15m-*row.PreviousReturn15m)
		}
	}
	if len(returns15) == 0 {
		return
	}
	acc.windows++
	acc.breadth15 = append(acc.breadth15, 100*float64(positive15)/float64(len(returns15)))
	acc.breadth1h = append(acc.breadth1h, 100*float64(positive1h)/float64(len(returns1h)))
	threshold15 := quantileCopy(returns15, 0.95)
	threshold1h := quantileCopy(returns1h, 0.90)
	acc.threshold15 = append(acc.threshold15, threshold15)
	acc.threshold1h = append(acc.threshold1h, threshold1h)
	floor15, floor1h := candidateFloors(sector)
	candidates := make(map[string]struct{})
	for _, row := range rows {
		if !allFinite(row.Return15m, row.Return1h) {
			continue
		}
		if row.Return15m >= math.Max(floor15, threshold15) || row.Return1h >= math.Max(floor1h, threshold1h) {
			candidates[row.Symbol] = struct{}{}
			acc.unique[row.Symbol] = struct{}{}
		}
	}
	acc.candidateCounts = append(acc.candidateCounts, float64(len(candidates)))
	if acc.previousCandidates != nil && rows[0].AsOf.Sub(acc.previousAsOf) == market.SnapshotInterval {
		entries, exits, union := setDifferenceCounts(acc.previousCandidates, candidates)
		acc.entries += entries
		acc.exits += exits
		acc.comparisons++
		if union == 0 {
			acc.turnovers = append(acc.turnovers, 0)
		} else {
			acc.turnovers = append(acc.turnovers, 100*float64(entries+exits)/float64(union))
		}
	}
	acc.previousCandidates = candidates
	acc.previousAsOf = rows[0].AsOf
}

func setDifferenceCounts(previous, current map[string]struct{}) (entries, exits, union int) {
	for symbol := range previous {
		if _, exists := current[symbol]; !exists {
			exits++
		}
	}
	for symbol := range current {
		if _, exists := previous[symbol]; !exists {
			entries++
		}
	}
	union = len(previous) + entries
	return entries, exits, union
}

func accumulateKlines(accumulators map[market.Sector]*sectorAccumulator, start, end time.Time, rows []KlineObservation) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Symbol == rows[j].Symbol {
			return rows[i].ClosedAt.Before(rows[j].ClosedAt)
		}
		return rows[i].Symbol < rows[j].Symbol
	})
	for index := 0; index < len(rows); {
		symbolEnd := index + 1
		for symbolEnd < len(rows) && rows[symbolEnd].Symbol == rows[index].Symbol {
			symbolEnd++
		}
		accumulateSymbolKlines(accumulators[rows[index].Sector], start, end, rows[index:symbolEnd])
		index = symbolEnd
	}
}

func accumulateSymbolKlines(acc *sectorAccumulator, start, end time.Time, rows []KlineObservation) {
	if acc == nil {
		return
	}
	positiveStreak := 0
	for index, row := range rows {
		if !allFinite(row.High, row.Close, row.QuoteVolume) || row.High <= 0 || row.Close <= 0 || row.QuoteVolume < 0 {
			positiveStreak = 0
			continue
		}
		if index > 0 && row.ClosedAt.Sub(rows[index-1].ClosedAt) == 15*time.Minute && row.Close > rows[index-1].Close {
			positiveStreak++
		} else {
			positiveStreak = 0
		}
		if row.ClosedAt.Before(start) || row.ClosedAt.After(end) {
			continue
		}
		acc.klineRows++
		acc.positiveStreak = append(acc.positiveStreak, float64(positiveStreak))
		windowStart := index - 3
		if windowStart < 0 {
			windowStart = 0
		}
		for windowStart < index && !contiguousKlines(rows, windowStart, index) {
			windowStart++
		}
		high := row.High
		for previous := windowStart; previous < index; previous++ {
			if rows[previous].High > high {
				high = rows[previous].High
			}
		}
		acc.drawdown1h = append(acc.drawdown1h, 100*(row.Close/high-1))
		if index >= 20 && contiguousKlines(rows, index-20, index) {
			previousVolumes := make([]float64, 20)
			for previous := index - 20; previous < index; previous++ {
				previousVolumes[previous-(index-20)] = rows[previous].QuoteVolume
			}
			median := quantileCopy(previousVolumes, 0.5)
			if median > 0 {
				acc.volumeExpansion = append(acc.volumeExpansion, row.QuoteVolume/median)
			}
		}
	}
}

func contiguousKlines(rows []KlineObservation, first, last int) bool {
	for index := first + 1; index <= last; index++ {
		if rows[index].ClosedAt.Sub(rows[index-1].ClosedAt) != 15*time.Minute {
			return false
		}
	}
	return true
}

func buildSectorReport(sector market.Sector, acc *sectorAccumulator) SectorReport {
	floor15, floor1h := candidateFloors(sector)
	return SectorReport{
		Sector: sector, FeatureRows: acc.featureRows, FeatureWindows: acc.windows,
		Return15m: distribution(acc.returns15), Return1h: distribution(acc.returns1h),
		Acceleration15m:     distribution(acc.acceleration),
		RecentQuoteVolume1h: distribution(acc.volume1h), QuoteVolume24h: distribution(acc.volume24h),
		PositiveBreadth15m: distribution(acc.breadth15), PositiveBreadth1h: distribution(acc.breadth1h),
		CrossSectionalP95Return15m: distribution(acc.threshold15), CrossSectionalP90Return1h: distribution(acc.threshold1h),
		Candidates: CandidateSimulation{
			Return15mAbsoluteFloor: floor15, Return1hAbsoluteFloor: floor1h,
			Return15mPercentile: 95, Return1hPercentile: 90,
			CountPerWindow: distribution(acc.candidateCounts), TurnoverPerComparison: distribution(acc.turnovers),
			UniqueSymbols: len(acc.unique), Entries: acc.entries, Exits: acc.exits, Comparisons: acc.comparisons,
		},
		Klines: KlineMetrics{
			Rows: acc.klineRows, VolumeExpansion20Median: distribution(acc.volumeExpansion),
			DrawdownFrom1hHigh: distribution(acc.drawdown1h), PositiveCloseStreak: distribution(acc.positiveStreak),
		},
	}
}

func distribution(values []float64) Distribution {
	clean := make([]float64, 0, len(values))
	sum := 0.0
	for _, value := range values {
		if allFinite(value) {
			clean = append(clean, value)
			sum += value
		}
	}
	if len(clean) == 0 {
		return Distribution{}
	}
	sort.Float64s(clean)
	return Distribution{
		Count: len(clean), Min: clean[0], P25: quantileSorted(clean, 0.25), P50: quantileSorted(clean, 0.50),
		P75: quantileSorted(clean, 0.75), P90: quantileSorted(clean, 0.90), P95: quantileSorted(clean, 0.95),
		P99: quantileSorted(clean, 0.99), Max: clean[len(clean)-1], Mean: sum / float64(len(clean)),
	}
}

func quantileCopy(values []float64, percentile float64) float64 {
	copyOfValues := append([]float64(nil), values...)
	sort.Float64s(copyOfValues)
	return quantileSorted(copyOfValues, percentile)
}

func quantileSorted(values []float64, percentile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	position := percentile * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return values[lower]
	}
	weight := position - float64(lower)
	return values[lower]*(1-weight) + values[upper]*weight
}

func allFinite(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return false
		}
	}
	return true
}
