package candidateanalysis

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

type fakeSource struct {
	latest   time.Time
	features []FeatureObservation
	klines   []KlineObservation
}

func (f fakeSource) LatestFeatureAsOf(context.Context, string) (time.Time, error) {
	return f.latest, nil
}

func (f fakeSource) FeatureObservations(context.Context, time.Time, time.Time, string) ([]FeatureObservation, error) {
	return append([]FeatureObservation(nil), f.features...), nil
}

func (f fakeSource) KlineObservations(context.Context, time.Time, time.Time) ([]KlineObservation, error) {
	return append([]KlineObservation(nil), f.klines...), nil
}

type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

func TestServiceAnalyzesCandidateTurnoverAndClosedKlines(t *testing.T) {
	end := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	start := end.Add(-time.Hour)
	firstWindow := end.Add(-5 * time.Minute)
	previous := 0.5
	features := []FeatureObservation{
		{Symbol: "AUSDT", Sector: market.SectorCrypto, AsOf: firstWindow, Return15m: 4, Return1h: 0, RecentQuoteVolume1h: 100, QuoteVolume24h: 1000},
		{Symbol: "BUSDT", Sector: market.SectorCrypto, AsOf: firstWindow, Return15m: 0, Return1h: 0, RecentQuoteVolume1h: 200, QuoteVolume24h: 2000},
		{Symbol: "CUSDT", Sector: market.SectorCrypto, AsOf: firstWindow, Return15m: -1, Return1h: -2, RecentQuoteVolume1h: 300, QuoteVolume24h: 3000},
		{Symbol: "AUSDT", Sector: market.SectorCrypto, AsOf: end, Return15m: 0, Return1h: 0, RecentQuoteVolume1h: 100, QuoteVolume24h: 1000},
		{Symbol: "BUSDT", Sector: market.SectorCrypto, AsOf: end, Return15m: 4, Return1h: 0, Previous15mAsOf: end.Add(-15 * time.Minute), PreviousReturn15m: &previous, RecentQuoteVolume1h: 200, QuoteVolume24h: 2000},
		{Symbol: "CUSDT", Sector: market.SectorCrypto, AsOf: end, Return15m: -1, Return1h: -2, RecentQuoteVolume1h: 300, QuoteVolume24h: 3000},
		{Symbol: "XAUUSDT", Sector: market.SectorTradFi, AsOf: firstWindow, Return15m: 0.1, Return1h: 0.2, RecentQuoteVolume1h: 50, QuoteVolume24h: 500},
		{Symbol: "XAUUSDT", Sector: market.SectorTradFi, AsOf: end, Return15m: 0.1, Return1h: 0.2, RecentQuoteVolume1h: 50, QuoteVolume24h: 500},
	}
	klines := make([]KlineObservation, 0, 25)
	closedAt := start.Add(-20 * 15 * time.Minute)
	for index := 0; index < 25; index++ {
		volume := 1.0
		if index == 20 {
			volume = 2
		}
		klines = append(klines, KlineObservation{
			Symbol: "AUSDT", Sector: market.SectorCrypto, ClosedAt: closedAt.Add(time.Duration(index) * 15 * time.Minute),
			High: 110, Close: 100 + float64(index), QuoteVolume: volume,
		})
	}
	service, err := NewService(fakeSource{latest: end, features: features, klines: klines}, fixedClock{now: end.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), Options{Lookback: time.Hour, FeatureVersion: market.ReturnFeatureVersion1})
	if err != nil {
		t.Fatal(err)
	}
	crypto := report.Sectors[0]
	if crypto.Sector != market.SectorCrypto || crypto.FeatureWindows != 2 || crypto.Candidates.UniqueSymbols != 2 {
		t.Fatalf("crypto report = %#v", crypto)
	}
	if crypto.Candidates.Entries != 1 || crypto.Candidates.Exits != 1 || crypto.Candidates.TurnoverPerComparison.P50 != 100 {
		t.Fatalf("candidate simulation = %#v", crypto.Candidates)
	}
	if crypto.Acceleration15m.Count != 1 || crypto.Acceleration15m.P50 != 3.5 {
		t.Fatalf("acceleration = %#v", crypto.Acceleration15m)
	}
	if crypto.Klines.VolumeExpansion20Median.Count == 0 || crypto.Klines.VolumeExpansion20Median.Max != 2 {
		t.Fatalf("kline metrics = %#v", crypto.Klines)
	}
	if report.End != end || report.Start != start || report.AnalysisVersion != AnalysisVersion1 {
		t.Fatalf("report metadata = %#v", report)
	}
}

func TestServiceRejectsUnsafeLookback(t *testing.T) {
	service, _ := NewService(fakeSource{}, fixedClock{})
	if _, err := service.Run(context.Background(), Options{Lookback: 15 * 24 * time.Hour, FeatureVersion: "returns-v1"}); err == nil {
		t.Fatal("expected lookback validation error")
	}
}

func TestServiceRejectsFutureOrMissingEndWindow(t *testing.T) {
	latest := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	service, _ := NewService(fakeSource{latest: latest}, fixedClock{})
	if _, err := service.Run(context.Background(), Options{
		End: latest.Add(5 * time.Minute), Lookback: time.Hour, FeatureVersion: market.ReturnFeatureVersion1,
	}); err == nil {
		t.Fatal("expected future end validation error")
	}
	service, _ = NewService(fakeSource{
		latest: latest,
		features: []FeatureObservation{{
			Symbol: "BTCUSDT", Sector: market.SectorCrypto, AsOf: latest.Add(-5 * time.Minute),
		}},
	}, fixedClock{})
	if _, err := service.Run(context.Background(), Options{
		End: latest, Lookback: time.Hour, FeatureVersion: market.ReturnFeatureVersion1,
	}); err == nil {
		t.Fatal("expected missing end window validation error")
	}
}

func TestDistributionUsesInterpolatedQuantiles(t *testing.T) {
	result := distribution([]float64{1, 2, 3, 4, 5})
	if result.Count != 5 || result.P50 != 3 || result.P90 != 4.6 || result.Mean != 3 {
		t.Fatalf("distribution = %#v", result)
	}
}

func TestCandidateTurnoverSkipsNonContiguousFeatureWindows(t *testing.T) {
	start := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	features := []FeatureObservation{
		{Symbol: "AUSDT", Sector: market.SectorCrypto, AsOf: start, Return15m: 4, Return1h: 0},
		{Symbol: "BUSDT", Sector: market.SectorCrypto, AsOf: start.Add(10 * time.Minute), Return15m: 4, Return1h: 0},
	}
	report, err := analyze(start, start.Add(10*time.Minute), market.ReturnFeatureVersion1, start, features, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Sectors[0].Candidates.Comparisons != 0 || report.Sectors[0].Candidates.TurnoverPerComparison.Count != 0 {
		t.Fatalf("candidates=%#v", report.Sectors[0].Candidates)
	}
}

func TestRenderersExposeResearchSemantics(t *testing.T) {
	report := Report{
		AnalysisVersion: AnalysisVersion1,
		FeatureVersion:  market.ReturnFeatureVersion1,
		Start:           time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
		End:             time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC),
		GeneratedAt:     time.Date(2026, 8, 28, 1, 1, 0, 0, time.UTC),
		Sectors:         []SectorReport{{Sector: market.SectorCrypto}},
		Notes:           []string{"只读研究统计"},
	}
	var markdown bytes.Buffer
	if err := RenderMarkdown(&markdown, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(markdown.String(), "R4-A0") || !strings.Contains(markdown.String(), "只读研究统计") {
		t.Fatalf("markdown = %s", markdown.String())
	}
	var jsonOutput bytes.Buffer
	if err := RenderJSON(&jsonOutput, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOutput.String(), `"analysis_version": "candidate-analysis-v1"`) {
		t.Fatalf("json = %s", jsonOutput.String())
	}
}
