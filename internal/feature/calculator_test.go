package feature

import (
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

func TestCalculatorComputesAllHorizonsWithExactEvidence(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	calculator := testCalculator(t)
	items, err := calculator.Calculate(asOf, []market.ReturnFeatureInput{completeInput("BTCUSDT", asOf)})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	item := items[0]
	if item.Symbol != "BTCUSDT" || item.CurrentPrice.String() != "196" ||
		!item.CurrentPriceAt.Equal(asOf) || item.RecentQuoteVolume1h.String() != "4000" ||
		item.QuoteVolume24h.String() != "96000" {
		t.Fatalf("item = %#v", item)
	}
	for _, horizon := range market.ReturnHorizons() {
		metric := item.Metrics[horizon]
		if !metric.IsValid || metric.InvalidReason != "" || metric.GapCount != 0 ||
			!metric.BaselinePriceAt.Equal(metric.TargetAt) {
			t.Fatalf("metric %s = %#v", horizon, metric)
		}
	}
	want15m := decimal.RequireFromString("0.51282051282051")
	if !item.Metrics[market.ReturnHorizon15m].ReturnPercent.Equal(want15m) {
		t.Fatalf("15m return = %s", item.Metrics[market.ReturnHorizon15m].ReturnPercent)
	}
	if item.Metrics[market.ReturnHorizon24h].ReturnPercent.String() != "96" {
		t.Fatalf("24h return = %s", item.Metrics[market.ReturnHorizon24h].ReturnPercent)
	}
	if err := (market.ReturnFeatureBatch{
		AsOf: asOf, FeatureVersion: market.ReturnFeatureVersion1,
		CalculatedAt: asOf.Add(time.Second), Items: items,
	}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCalculatorQualityGateReasons(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		mutate  func(*market.ReturnFeatureInput)
		horizon market.ReturnHorizon
		reason  string
	}{
		{
			name: "current missing", horizon: market.ReturnHorizon1h, reason: InvalidCurrentMissing,
			mutate: func(input *market.ReturnFeatureInput) { input.Prices = nil },
		},
		{
			name: "current stale", horizon: market.ReturnHorizon1h, reason: InvalidCurrentStale,
			mutate: func(input *market.ReturnFeatureInput) { input.Prices = input.Prices[:len(input.Prices)-1] },
		},
		{
			name: "current low quality", horizon: market.ReturnHorizon1h, reason: InvalidCurrentLowQuality,
			mutate: func(input *market.ReturnFeatureInput) { input.Prices[len(input.Prices)-1].QualityScore = 50 },
		},
		{
			name: "baseline missing for new listing", horizon: market.ReturnHorizon24h, reason: InvalidBaselineMissing,
			mutate: func(input *market.ReturnFeatureInput) { input.Prices = input.Prices[len(input.Prices)-20:] },
		},
		{
			name: "baseline too old", horizon: market.ReturnHorizon1h, reason: InvalidBaselineTooOld,
			mutate: func(input *market.ReturnFeatureInput) {
				target := asOf.Add(-time.Hour)
				filtered := input.Prices[:0]
				for _, point := range input.Prices {
					if point.ObservedAt.Equal(target) {
						continue
					}
					filtered = append(filtered, point)
				}
				input.Prices = filtered
			},
		},
		{
			name: "baseline low quality", horizon: market.ReturnHorizon1h, reason: InvalidBaselineLowQuality,
			mutate: func(input *market.ReturnFeatureInput) {
				target := asOf.Add(-time.Hour)
				for index := range input.Prices {
					if input.Prices[index].ObservedAt.Equal(target) {
						input.Prices[index].QualityScore = 50
					}
				}
			},
		},
		{
			name: "middle kline gap", horizon: market.ReturnHorizon4h, reason: InvalidKlineGaps,
			mutate: func(input *market.ReturnFeatureInput) {
				missing := asOf.Add(-2 * time.Hour)
				filtered := input.Klines[:0]
				for _, point := range input.Klines {
					if !point.CloseAt.Equal(missing) {
						filtered = append(filtered, point)
					}
				}
				input.Klines = filtered
			},
		},
		{
			name: "no recent liquidity", horizon: market.ReturnHorizon15m, reason: InvalidNoRecentLiquidity,
			mutate: func(input *market.ReturnFeatureInput) {
				for index := range input.Klines {
					input.Klines[index].QuoteVolume = decimal.Zero
					input.Klines[index].TradeCount = 0
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeInput("BTCUSDT", asOf)
			test.mutate(&input)
			items, err := testCalculator(t).Calculate(asOf, []market.ReturnFeatureInput{input})
			if err != nil {
				t.Fatal(err)
			}
			metric := items[0].Metrics[test.horizon]
			if metric.IsValid || metric.InvalidReason != test.reason {
				t.Fatalf("metric = %#v", metric)
			}
		})
	}
}

func TestCalculatorReturnDirections(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		currentPrice string
		wantSign     int
	}{
		{name: "up", currentPrice: "196", wantSign: 1},
		{name: "flat", currentPrice: "195", wantSign: 0},
		{name: "down", currentPrice: "194", wantSign: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := completeInput("BTCUSDT", asOf)
			input.Prices[len(input.Prices)-1].Price = decimal.RequireFromString(test.currentPrice)
			items, err := testCalculator(t).Calculate(asOf, []market.ReturnFeatureInput{input})
			if err != nil {
				t.Fatal(err)
			}
			metric := items[0].Metrics[market.ReturnHorizon15m]
			if !metric.IsValid || metric.ReturnPercent.Sign() != test.wantSign {
				t.Fatalf("metric = %#v", metric)
			}
		})
	}
}

func TestCalculatorAcceptsFiveMinuteBoundaryOffsets(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 5, 0, 0, time.UTC)
	input := completeInput("BTCUSDT", asOf.Add(-5*time.Minute))
	input.Prices = append(input.Prices, market.FeaturePricePoint{
		ObservedAt: asOf, Price: decimal.NewFromInt(200), Source: market.PriceSourceSnapshot5m, QualityScore: 100,
	})
	items, err := testCalculator(t).Calculate(asOf, []market.ReturnFeatureInput{input})
	if err != nil {
		t.Fatal(err)
	}
	item := items[0]
	if item.CurrentPrice.String() != "200" || item.CurrentSource != market.PriceSourceSnapshot5m {
		t.Fatalf("current = %s/%s", item.CurrentPrice, item.CurrentSource)
	}
	metric := item.Metrics[market.ReturnHorizon1h]
	if !metric.IsValid || metric.BaselineOffsetSeconds != 5*60 {
		t.Fatalf("metric = %#v", metric)
	}
}

func TestCalculatorPrefersKlineAtSameBoundary(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	input := completeInput("BTCUSDT", asOf)
	input.Prices = append(input.Prices, market.FeaturePricePoint{
		ObservedAt: asOf, Price: decimal.NewFromInt(999), Source: market.PriceSourceSnapshot5m, QualityScore: 100,
	})
	items, err := testCalculator(t).Calculate(asOf, []market.ReturnFeatureInput{input})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].CurrentPrice.String() != "196" || items[0].CurrentSource != market.PriceSourceKline15m {
		t.Fatalf("current = %s/%s", items[0].CurrentPrice, items[0].CurrentSource)
	}
}

func TestCalculatorRejectsUnalignedAsOfAndDuplicateSymbols(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 1, 0, time.UTC)
	calculator := testCalculator(t)
	if _, err := calculator.Calculate(asOf, nil); err == nil {
		t.Fatal("expected alignment error")
	}
	aligned := asOf.Truncate(5 * time.Minute)
	input := completeInput("BTCUSDT", aligned)
	if _, err := calculator.Calculate(aligned, []market.ReturnFeatureInput{input, input}); err == nil {
		t.Fatal("expected duplicate error")
	}
}

func testCalculator(t *testing.T) *Calculator {
	t.Helper()
	calculator, err := NewCalculator(Policy{
		CurrentMaxAge: 5 * time.Minute, BaselineMaxOffset: 5 * time.Minute,
		MinimumQuality: 75, LiquidityLookback: time.Hour,
		FeatureVersion: market.ReturnFeatureVersion1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return calculator
}

func completeInput(symbol string, asOf time.Time) market.ReturnFeatureInput {
	start := asOf.Add(-24 * time.Hour)
	prices := make([]market.FeaturePricePoint, 0, 97)
	klines := make([]market.FeatureKlinePoint, 0, 97)
	price := int64(100)
	for observedAt := start; !observedAt.After(asOf); observedAt = observedAt.Add(15 * time.Minute) {
		prices = append(prices, market.FeaturePricePoint{
			ObservedAt: observedAt, Price: decimal.NewFromInt(price),
			Source: market.PriceSourceKline15m, QualityScore: 100,
		})
		klines = append(klines, market.FeatureKlinePoint{
			CloseAt: observedAt, QuoteVolume: decimal.NewFromInt(1000), TradeCount: 10,
		})
		price++
	}
	return market.ReturnFeatureInput{Symbol: symbol, Sector: market.SectorCrypto, Prices: prices, Klines: klines}
}
