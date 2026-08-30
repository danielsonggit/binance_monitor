package market

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	ReturnFeatureVersion1 = "returns-v1"
	PriceSourceKline15m   = "KLINE_15M"
	PriceSourceSnapshot5m = "SNAPSHOT_5M"
)

type ReturnHorizon string

const (
	ReturnHorizon15m ReturnHorizon = "15m"
	ReturnHorizon1h  ReturnHorizon = "1h"
	ReturnHorizon4h  ReturnHorizon = "4h"
	ReturnHorizon24h ReturnHorizon = "24h"
)

var allReturnHorizons = []ReturnHorizon{
	ReturnHorizon15m,
	ReturnHorizon1h,
	ReturnHorizon4h,
	ReturnHorizon24h,
}

func ReturnHorizons() []ReturnHorizon {
	return append([]ReturnHorizon(nil), allReturnHorizons...)
}

func (h ReturnHorizon) Duration() (time.Duration, error) {
	switch h {
	case ReturnHorizon15m:
		return 15 * time.Minute, nil
	case ReturnHorizon1h:
		return time.Hour, nil
	case ReturnHorizon4h:
		return 4 * time.Hour, nil
	case ReturnHorizon24h:
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("不支持的收益周期 %q", h)
	}
}

type FeaturePricePoint struct {
	ObservedAt   time.Time
	Price        decimal.Decimal
	Source       string
	QualityScore int16
}

type FeatureKlinePoint struct {
	CloseAt     time.Time
	QuoteVolume decimal.Decimal
	TradeCount  int64
}

type ReturnFeatureInput struct {
	Symbol string
	Sector Sector
	Prices []FeaturePricePoint
	Klines []FeatureKlinePoint
}

type ReturnMetric struct {
	Horizon               ReturnHorizon
	TargetAt              time.Time
	BaselinePrice         decimal.Decimal
	BaselinePriceAt       time.Time
	BaselineSource        string
	BaselineOffsetSeconds int64
	ReturnPercent         decimal.Decimal
	GapCount              int
	IsValid               bool
	InvalidReason         string
}

type ReturnFeatureSet struct {
	Symbol              string
	Sector              Sector
	AsOf                time.Time
	FeatureVersion      string
	CurrentPrice        decimal.Decimal
	CurrentPriceAt      time.Time
	CurrentSource       string
	CurrentAgeSeconds   int64
	RecentQuoteVolume1h decimal.Decimal
	QuoteVolume24h      decimal.Decimal
	Metrics             map[ReturnHorizon]ReturnMetric
}

type ReturnFeatureBatch struct {
	AsOf           time.Time
	FeatureVersion string
	CalculatedAt   time.Time
	Items          []ReturnFeatureSet
}

type ReturnFeatureWriteResult struct {
	Attempted int
	Upserted  int
}

func (b ReturnFeatureBatch) Validate() error {
	if b.AsOf.IsZero() || !b.AsOf.Equal(b.AsOf.UTC().Truncate(SnapshotInterval)) {
		return fmt.Errorf("feature batch as_of 必须按 %s UTC 对齐", SnapshotInterval)
	}
	if strings.TrimSpace(b.FeatureVersion) == "" || b.CalculatedAt.IsZero() {
		return fmt.Errorf("feature batch version/calculated_at 不能为空")
	}
	if len(b.Items) == 0 {
		return fmt.Errorf("feature batch 不能为空")
	}
	seen := make(map[string]struct{}, len(b.Items))
	for _, item := range b.Items {
		if err := item.Validate(); err != nil {
			return err
		}
		if !item.AsOf.Equal(b.AsOf) || item.FeatureVersion != b.FeatureVersion {
			return fmt.Errorf("feature %s 与 batch 时间或版本不一致", item.Symbol)
		}
		if _, exists := seen[item.Symbol]; exists {
			return fmt.Errorf("feature batch 存在重复 symbol %s", item.Symbol)
		}
		seen[item.Symbol] = struct{}{}
	}
	return nil
}

func (f ReturnFeatureSet) Validate() error {
	if strings.TrimSpace(f.Symbol) == "" || f.Symbol != strings.ToUpper(strings.TrimSpace(f.Symbol)) {
		return fmt.Errorf("feature symbol 必须是规范化的大写值")
	}
	if f.Sector != SectorCrypto && f.Sector != SectorTradFi {
		return fmt.Errorf("feature %s sector 无效", f.Symbol)
	}
	if f.AsOf.IsZero() || strings.TrimSpace(f.FeatureVersion) == "" {
		return fmt.Errorf("feature %s as_of/version 不能为空", f.Symbol)
	}
	if f.CurrentPriceAt.IsZero() != f.CurrentPrice.IsZero() {
		return fmt.Errorf("feature %s 当前价格和值时间必须同时存在或缺失", f.Symbol)
	}
	if !f.CurrentPrice.IsZero() && !f.CurrentPrice.IsPositive() {
		return fmt.Errorf("feature %s 当前价格必须大于 0", f.Symbol)
	}
	if !f.CurrentPriceAt.IsZero() && f.CurrentSource != PriceSourceKline15m && f.CurrentSource != PriceSourceSnapshot5m {
		return fmt.Errorf("feature %s 当前价格 source 无效", f.Symbol)
	}
	if !f.CurrentPriceAt.IsZero() {
		if f.CurrentPriceAt.After(f.AsOf) || f.CurrentAgeSeconds != int64(f.AsOf.Sub(f.CurrentPriceAt)/time.Second) {
			return fmt.Errorf("feature %s 当前价格时间或 age 不一致", f.Symbol)
		}
	} else if f.CurrentSource != "" || f.CurrentAgeSeconds != 0 {
		return fmt.Errorf("feature %s 缺少当前价格时 source/age 必须为空", f.Symbol)
	}
	if f.CurrentAgeSeconds < 0 || f.RecentQuoteVolume1h.IsNegative() || f.QuoteVolume24h.IsNegative() {
		return fmt.Errorf("feature %s age/volume 无效", f.Symbol)
	}
	if len(f.Metrics) != len(allReturnHorizons) {
		return fmt.Errorf("feature %s 必须包含全部收益周期", f.Symbol)
	}
	for _, horizon := range allReturnHorizons {
		metric, exists := f.Metrics[horizon]
		if !exists || metric.Horizon != horizon {
			return fmt.Errorf("feature %s 缺少周期 %s", f.Symbol, horizon)
		}
		if err := metric.Validate(f.AsOf); err != nil {
			return fmt.Errorf("feature %s/%s: %w", f.Symbol, horizon, err)
		}
	}
	return nil
}

func (m ReturnMetric) Validate(asOf time.Time) error {
	duration, err := m.Horizon.Duration()
	if err != nil {
		return err
	}
	if !m.TargetAt.Equal(asOf.Add(-duration)) {
		return fmt.Errorf("target_at 与周期不匹配")
	}
	if m.GapCount < 0 || m.BaselineOffsetSeconds < 0 {
		return fmt.Errorf("gap/offset 不能为负数")
	}
	if m.BaselinePriceAt.IsZero() != m.BaselinePrice.IsZero() {
		return fmt.Errorf("基准价格和值时间必须同时存在或缺失")
	}
	if !m.BaselinePrice.IsZero() && !m.BaselinePrice.IsPositive() {
		return fmt.Errorf("基准价格必须大于 0")
	}
	if !m.BaselinePriceAt.IsZero() && m.BaselineSource != PriceSourceKline15m && m.BaselineSource != PriceSourceSnapshot5m {
		return fmt.Errorf("基准价格 source 无效")
	}
	if !m.BaselinePriceAt.IsZero() {
		if m.BaselinePriceAt.After(m.TargetAt) ||
			m.BaselineOffsetSeconds != int64(m.TargetAt.Sub(m.BaselinePriceAt)/time.Second) {
			return fmt.Errorf("基准价格时间或 offset 不一致")
		}
	} else if m.BaselineSource != "" || m.BaselineOffsetSeconds != 0 {
		return fmt.Errorf("缺少基准价格时 source/offset 必须为空")
	}
	if m.IsValid {
		if m.InvalidReason != "" || m.BaselinePriceAt.IsZero() {
			return fmt.Errorf("有效收益不得包含 invalid reason 或缺少基准")
		}
	} else {
		if strings.TrimSpace(m.InvalidReason) == "" {
			return fmt.Errorf("无效收益必须包含 invalid reason")
		}
		if !m.ReturnPercent.IsZero() {
			return fmt.Errorf("无效收益不得包含 return percent")
		}
	}
	return nil
}
