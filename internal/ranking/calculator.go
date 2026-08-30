package ranking

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

type Policy struct {
	RankingVersion string
	FeatureVersion string
	TopN           int
}

type Calculator struct {
	policy Policy
}

func NewCalculator(policy Policy) (*Calculator, error) {
	if strings.TrimSpace(policy.RankingVersion) == "" || strings.TrimSpace(policy.FeatureVersion) == "" {
		return nil, fmt.Errorf("ranking/feature version 不能为空")
	}
	if policy.TopN <= 0 || policy.TopN > 100 {
		return nil, fmt.Errorf("ranking TopN 必须在 1 到 100 之间")
	}
	return &Calculator{policy: policy}, nil
}

func (c *Calculator) Calculate(asOf time.Time, inputs []market.RankingInput) ([]market.RankingGroup, error) {
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) {
		return nil, fmt.Errorf("ranking as_of 必须按 %s UTC 对齐", market.SnapshotInterval)
	}
	if len(inputs) == 0 {
		return nil, fmt.Errorf("ranking inputs 不能为空")
	}
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if err := validateInput(input); err != nil {
			return nil, err
		}
		if _, exists := seen[input.Symbol]; exists {
			return nil, fmt.Errorf("ranking inputs 存在重复 symbol %s", input.Symbol)
		}
		seen[input.Symbol] = struct{}{}
	}

	sectors := []market.Sector{market.SectorCrypto, market.SectorTradFi}
	groups := make([]market.RankingGroup, 0, len(sectors)*len(market.ReturnHorizons()))
	for _, sector := range sectors {
		activeCount := countSector(inputs, sector)
		for _, horizon := range market.ReturnHorizons() {
			eligibleCount := countEligible(inputs, sector, horizon)
			candidates := positiveCandidatesFor(inputs, sector, horizon)
			sort.Slice(candidates, func(i, j int) bool {
				if comparison := candidates[i].ReturnPercent.Cmp(candidates[j].ReturnPercent); comparison != 0 {
					return comparison > 0
				}
				if comparison := candidates[i].QuoteVolume24h.Cmp(candidates[j].QuoteVolume24h); comparison != 0 {
					return comparison > 0
				}
				return candidates[i].Symbol < candidates[j].Symbol
			})
			positiveCount := len(candidates)
			limit := min(c.policy.TopN, positiveCount)
			items := make([]market.RankingEntry, 0, limit)
			for index := 0; index < limit; index++ {
				candidate := candidates[index]
				items = append(items, market.RankingEntry{
					Rank:           index + 1,
					Symbol:         candidate.Symbol,
					ReturnPercent:  candidate.ReturnPercent,
					CurrentPrice:   candidate.CurrentPrice,
					QuoteVolume24h: candidate.QuoteVolume24h,
					Percentile:     percentile(index+1, eligibleCount),
				})
			}
			groups = append(groups, market.RankingGroup{
				AsOf: asOf, RankingVersion: c.policy.RankingVersion, FeatureVersion: c.policy.FeatureVersion,
				Sector: sector, Horizon: horizon, RequestedLimit: c.policy.TopN,
				ActiveCount: activeCount, EligibleCount: eligibleCount, PositiveCount: positiveCount, Items: items,
			})
		}
	}
	return groups, nil
}

func validateInput(input market.RankingInput) error {
	if strings.TrimSpace(input.Symbol) == "" || input.Symbol != strings.ToUpper(strings.TrimSpace(input.Symbol)) {
		return fmt.Errorf("ranking input symbol 必须是规范化的大写值")
	}
	if input.Sector != market.SectorCrypto && input.Sector != market.SectorTradFi {
		return fmt.Errorf("ranking input %s sector 无效", input.Symbol)
	}
	if input.CurrentPrice.IsNegative() || input.QuoteVolume24h.IsNegative() {
		return fmt.Errorf("ranking input %s 价格或成交额不能为负数", input.Symbol)
	}
	if len(input.Metrics) != len(market.ReturnHorizons()) {
		return fmt.Errorf("ranking input %s 必须包含全部收益周期", input.Symbol)
	}
	for _, horizon := range market.ReturnHorizons() {
		metric, exists := input.Metrics[horizon]
		if !exists || metric.Horizon != horizon {
			return fmt.Errorf("ranking input %s 缺少周期 %s", input.Symbol, horizon)
		}
		if metric.IsValid && !input.CurrentPrice.IsPositive() {
			return fmt.Errorf("ranking input %s 有效收益缺少正数当前价", input.Symbol)
		}
		if !metric.IsValid && !metric.ReturnPercent.IsZero() {
			return fmt.Errorf("ranking input %s/%s 无效收益不得包含数值", input.Symbol, horizon)
		}
	}
	return nil
}

func countSector(inputs []market.RankingInput, sector market.Sector) int {
	count := 0
	for _, input := range inputs {
		if input.Sector == sector {
			count++
		}
	}
	return count
}

func countEligible(inputs []market.RankingInput, sector market.Sector, horizon market.ReturnHorizon) int {
	count := 0
	for _, input := range inputs {
		if input.Sector == sector && input.Metrics[horizon].IsValid {
			count++
		}
	}
	return count
}

func positiveCandidatesFor(inputs []market.RankingInput, sector market.Sector, horizon market.ReturnHorizon) []market.RankingEntry {
	items := make([]market.RankingEntry, 0, len(inputs))
	for _, input := range inputs {
		metric := input.Metrics[horizon]
		if input.Sector != sector || !metric.IsValid || !metric.ReturnPercent.IsPositive() {
			continue
		}
		items = append(items, market.RankingEntry{
			Symbol: input.Symbol, ReturnPercent: metric.ReturnPercent,
			CurrentPrice: input.CurrentPrice, QuoteVolume24h: input.QuoteVolume24h,
		})
	}
	return items
}

func percentile(rank, eligible int) decimal.Decimal {
	if eligible <= 1 {
		return decimal.NewFromInt(100)
	}
	return decimal.NewFromInt(int64(eligible - rank)).
		Div(decimal.NewFromInt(int64(eligible - 1))).
		Mul(decimal.NewFromInt(100)).Round(6)
}
