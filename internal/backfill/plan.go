package backfill

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"
)

type Gap struct {
	Symbol string    `json:"symbol"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Count  int       `json:"count"`
}

type Plan struct {
	WindowStart time.Time
	WindowEnd   time.Time
	Expected    int
	Present     int
	Gaps        []Gap
}

// BuildPlan returns half-open missing ranges [Start, End) for completed
// candles. It examines every expected open time, so a gap in the middle is not
// hidden by a later maximum open time.
func BuildPlan(
	now time.Time,
	lookback time.Duration,
	interval market.KlineInterval,
	symbols []string,
	existing map[string][]time.Time,
) (Plan, error) {
	duration, err := interval.Duration()
	if err != nil {
		return Plan{}, err
	}
	if lookback <= 0 || lookback%duration != 0 {
		return Plan{}, fmt.Errorf("回补 lookback 必须是 %s 的正整数倍", interval)
	}
	normalized, err := normalizeSymbols(symbols)
	if err != nil {
		return Plan{}, err
	}
	windowEnd := now.UTC().Truncate(duration)
	windowStart := windowEnd.Add(-lookback)
	perSymbol := int(lookback / duration)
	plan := Plan{
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		Expected:    len(normalized) * perSymbol,
	}

	for _, symbol := range normalized {
		present := make(map[int64]struct{}, len(existing[symbol]))
		for _, openTime := range existing[symbol] {
			openTime = openTime.UTC()
			if openTime.Before(windowStart) || !openTime.Before(windowEnd) {
				continue
			}
			if !openTime.Equal(openTime.Truncate(duration)) {
				return Plan{}, fmt.Errorf("%s 已有 K 线时间未对齐：%s", symbol, openTime.Format(time.RFC3339Nano))
			}
			present[openTime.UnixMilli()] = struct{}{}
		}
		plan.Present += len(present)
		var current *Gap
		for openTime := windowStart; openTime.Before(windowEnd); openTime = openTime.Add(duration) {
			if _, ok := present[openTime.UnixMilli()]; ok {
				if current != nil {
					plan.Gaps = append(plan.Gaps, *current)
					current = nil
				}
				continue
			}
			if current == nil {
				current = &Gap{Symbol: symbol, Start: openTime, End: openTime.Add(duration), Count: 1}
			} else {
				current.End = openTime.Add(duration)
				current.Count++
			}
		}
		if current != nil {
			plan.Gaps = append(plan.Gaps, *current)
		}
	}
	return plan, nil
}

func normalizeSymbols(symbols []string) ([]string, error) {
	seen := make(map[string]struct{}, len(symbols))
	result := make([]string, 0, len(symbols))
	for _, raw := range symbols {
		symbol := strings.ToUpper(strings.TrimSpace(raw))
		if symbol == "" {
			return nil, fmt.Errorf("回补 symbol 不能为空")
		}
		if _, exists := seen[symbol]; exists {
			return nil, fmt.Errorf("回补 symbol 重复：%s", symbol)
		}
		seen[symbol] = struct{}{}
		result = append(result, symbol)
	}
	sort.Strings(result)
	return result, nil
}
