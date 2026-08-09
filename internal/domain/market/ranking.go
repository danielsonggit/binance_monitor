package market

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const RankingVersion1 = "ranking-v1"

type RankingMetricInput struct {
	Horizon       ReturnHorizon
	ReturnPercent decimal.Decimal
	IsValid       bool
}

type RankingInput struct {
	Symbol         string
	Sector         Sector
	CurrentPrice   decimal.Decimal
	QuoteVolume24h decimal.Decimal
	Metrics        map[ReturnHorizon]RankingMetricInput
}

type RankingEntry struct {
	Rank           int
	Symbol         string
	ReturnPercent  decimal.Decimal
	CurrentPrice   decimal.Decimal
	QuoteVolume24h decimal.Decimal
	Percentile     decimal.Decimal
}

type RankingGroup struct {
	AsOf           time.Time
	RankingVersion string
	FeatureVersion string
	Sector         Sector
	Horizon        ReturnHorizon
	RequestedLimit int
	ActiveCount    int
	EligibleCount  int
	PositiveCount  int
	Items          []RankingEntry
}

type RankingBatch struct {
	AsOf           time.Time
	RankingVersion string
	FeatureVersion string
	CalculatedAt   time.Time
	Groups         []RankingGroup
}

type RankingWriteResult struct {
	GroupsUpserted int
	ItemsWritten   int
}

func (b RankingBatch) Validate() error {
	if b.AsOf.IsZero() || !b.AsOf.Equal(b.AsOf.UTC().Truncate(SnapshotInterval)) {
		return fmt.Errorf("ranking batch as_of 必须按 %s UTC 对齐", SnapshotInterval)
	}
	if strings.TrimSpace(b.RankingVersion) == "" || strings.TrimSpace(b.FeatureVersion) == "" || b.CalculatedAt.IsZero() {
		return fmt.Errorf("ranking batch version/calculated_at 不能为空")
	}
	expected := len(ReturnHorizons()) * 2
	if len(b.Groups) != expected {
		return fmt.Errorf("ranking batch 必须包含 %d 个板块周期组合", expected)
	}
	seen := make(map[string]struct{}, expected)
	for _, group := range b.Groups {
		if err := group.Validate(); err != nil {
			return err
		}
		if !group.AsOf.Equal(b.AsOf) || group.RankingVersion != b.RankingVersion || group.FeatureVersion != b.FeatureVersion {
			return fmt.Errorf("ranking group %s/%s 与 batch 时间或版本不一致", group.Sector, group.Horizon)
		}
		key := string(group.Sector) + ":" + string(group.Horizon)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("ranking batch 存在重复组合 %s", key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (g RankingGroup) Validate() error {
	if g.AsOf.IsZero() || strings.TrimSpace(g.RankingVersion) == "" || strings.TrimSpace(g.FeatureVersion) == "" {
		return fmt.Errorf("ranking group 时间或版本不能为空")
	}
	if g.Sector != SectorCrypto && g.Sector != SectorTradFi {
		return fmt.Errorf("ranking group sector 无效: %q", g.Sector)
	}
	if _, err := g.Horizon.Duration(); err != nil {
		return err
	}
	if g.RequestedLimit <= 0 || g.ActiveCount < 0 || g.EligibleCount < 0 || g.PositiveCount < 0 ||
		g.EligibleCount > g.ActiveCount || g.PositiveCount > g.EligibleCount ||
		len(g.Items) > g.PositiveCount || len(g.Items) > g.RequestedLimit {
		return fmt.Errorf("ranking group %s/%s 计数无效", g.Sector, g.Horizon)
	}
	seen := make(map[string]struct{}, len(g.Items))
	for index, item := range g.Items {
		if item.Rank != index+1 {
			return fmt.Errorf("ranking group %s/%s rank 必须从 1 连续递增", g.Sector, g.Horizon)
		}
		if strings.TrimSpace(item.Symbol) == "" || item.Symbol != strings.ToUpper(strings.TrimSpace(item.Symbol)) {
			return fmt.Errorf("ranking item symbol 必须是规范化的大写值")
		}
		if !item.CurrentPrice.IsPositive() || item.QuoteVolume24h.IsNegative() {
			return fmt.Errorf("ranking item %s 的价格或成交额无效", item.Symbol)
		}
		if !item.ReturnPercent.IsPositive() {
			return fmt.Errorf("ranking item %s 必须是正收益", item.Symbol)
		}
		if item.Percentile.IsNegative() || item.Percentile.GreaterThan(decimal.NewFromInt(100)) {
			return fmt.Errorf("ranking item %s percentile 无效", item.Symbol)
		}
		if _, exists := seen[item.Symbol]; exists {
			return fmt.Errorf("ranking group 存在重复 symbol %s", item.Symbol)
		}
		seen[item.Symbol] = struct{}{}
	}
	return nil
}
