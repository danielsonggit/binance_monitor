package ranking

import (
	"reflect"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

func TestCalculatorBuildsStableSectorHorizonRankings(t *testing.T) {
	calculator := testCalculator(t, 3)
	asOf := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	inputs := []market.RankingInput{
		rankingInput("ZZZUSDT", market.SectorCrypto, "2", "100", true),
		rankingInput("AAAUSDT", market.SectorCrypto, "2", "100", true),
		rankingInput("BBBUSD", market.SectorCrypto, "2", "200", true),
		rankingInput("NEGUSDT", market.SectorCrypto, "-1", "1000", true),
		rankingInput("XAUUSDT", market.SectorTradFi, "0.5", "500", true),
		rankingInput("SILVERUSDT", market.SectorTradFi, "0", "600", false),
	}
	groups, err := calculator.Calculate(asOf, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 8 {
		t.Fatalf("groups=%d", len(groups))
	}
	crypto15m := findGroup(t, groups, market.SectorCrypto, market.ReturnHorizon15m)
	if crypto15m.ActiveCount != 4 || crypto15m.EligibleCount != 4 || crypto15m.PositiveCount != 3 || len(crypto15m.Items) != 3 {
		t.Fatalf("crypto group=%#v", crypto15m)
	}
	got := []string{crypto15m.Items[0].Symbol, crypto15m.Items[1].Symbol, crypto15m.Items[2].Symbol}
	if want := []string{"BBBUSD", "AAAUSDT", "ZZZUSDT"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("symbols=%v want=%v", got, want)
	}
	if !crypto15m.Items[0].Percentile.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("top percentile=%s", crypto15m.Items[0].Percentile)
	}
	tradFi15m := findGroup(t, groups, market.SectorTradFi, market.ReturnHorizon15m)
	if tradFi15m.ActiveCount != 2 || tradFi15m.EligibleCount != 1 || tradFi15m.PositiveCount != 1 || len(tradFi15m.Items) != 1 ||
		tradFi15m.Items[0].Symbol != "XAUUSDT" || !tradFi15m.Items[0].Percentile.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("tradfi group=%#v", tradFi15m)
	}
}

func TestCalculatorDoesNotFillGainersWithFlatOrNegativeReturns(t *testing.T) {
	calculator := testCalculator(t, 5)
	asOf := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	groups, err := calculator.Calculate(asOf, []market.RankingInput{
		rankingInput("UPUSDT", market.SectorCrypto, "0.1", "10", true),
		rankingInput("FLATUSDT", market.SectorCrypto, "0", "100", true),
		rankingInput("DOWNUSDT", market.SectorCrypto, "-1", "1000", true),
		rankingInput("XAUUSDT", market.SectorTradFi, "-0.1", "20", true),
	})
	if err != nil {
		t.Fatal(err)
	}
	crypto := findGroup(t, groups, market.SectorCrypto, market.ReturnHorizon15m)
	if crypto.EligibleCount != 3 || crypto.PositiveCount != 1 || len(crypto.Items) != 1 || crypto.Items[0].Symbol != "UPUSDT" {
		t.Fatalf("crypto=%#v", crypto)
	}
	tradFi := findGroup(t, groups, market.SectorTradFi, market.ReturnHorizon15m)
	if tradFi.EligibleCount != 1 || tradFi.PositiveCount != 0 || len(tradFi.Items) != 0 {
		t.Fatalf("tradfi=%#v", tradFi)
	}
}

func TestCalculatorIsDeterministicForInputOrder(t *testing.T) {
	calculator := testCalculator(t, 5)
	asOf := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	first := rankingInput("AUSDT", market.SectorCrypto, "1", "10", true)
	second := rankingInput("BUSDT", market.SectorCrypto, "2", "10", true)
	groupsA, err := calculator.Calculate(asOf, []market.RankingInput{first, second})
	if err != nil {
		t.Fatal(err)
	}
	groupsB, err := calculator.Calculate(asOf, []market.RankingInput{second, first})
	if err != nil {
		t.Fatal(err)
	}
	a := findGroup(t, groupsA, market.SectorCrypto, market.ReturnHorizon1h)
	b := findGroup(t, groupsB, market.SectorCrypto, market.ReturnHorizon1h)
	if !reflect.DeepEqual(a.Items, b.Items) {
		t.Fatalf("items differ: %#v %#v", a.Items, b.Items)
	}
}

func TestCalculatorRejectsInvalidMetricWithDefaultValueLeak(t *testing.T) {
	calculator := testCalculator(t, 5)
	input := rankingInput("BTCUSDT", market.SectorCrypto, "1", "10", true)
	metric := input.Metrics[market.ReturnHorizon24h]
	metric.IsValid = false
	input.Metrics[market.ReturnHorizon24h] = metric
	if _, err := calculator.Calculate(time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC), []market.RankingInput{input}); err == nil {
		t.Fatal("expected invalid metric value error")
	}
}

func testCalculator(t *testing.T, topN int) *Calculator {
	t.Helper()
	calculator, err := NewCalculator(Policy{
		RankingVersion: market.RankingVersion1,
		FeatureVersion: market.ReturnFeatureVersion1,
		TopN:           topN,
	})
	if err != nil {
		t.Fatal(err)
	}
	return calculator
}

func rankingInput(symbol string, sector market.Sector, returnValue, volume string, valid bool) market.RankingInput {
	metrics := make(map[market.ReturnHorizon]market.RankingMetricInput)
	for _, horizon := range market.ReturnHorizons() {
		value := decimal.Zero
		if valid {
			value = decimal.RequireFromString(returnValue)
		}
		metrics[horizon] = market.RankingMetricInput{Horizon: horizon, ReturnPercent: value, IsValid: valid}
	}
	return market.RankingInput{
		Symbol: symbol, Sector: sector, CurrentPrice: decimal.NewFromInt(10),
		QuoteVolume24h: decimal.RequireFromString(volume), Metrics: metrics,
	}
}

func findGroup(t *testing.T, groups []market.RankingGroup, sector market.Sector, horizon market.ReturnHorizon) market.RankingGroup {
	t.Helper()
	for _, group := range groups {
		if group.Sector == sector && group.Horizon == horizon {
			return group
		}
	}
	t.Fatalf("missing group %s/%s", sector, horizon)
	return market.RankingGroup{}
}
