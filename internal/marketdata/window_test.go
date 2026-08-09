package marketdata

import (
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

func TestWindowStoreOrdersUpsertsAndPrunesPoints(t *testing.T) {
	store := NewWindowStore(2 * time.Minute)
	t0 := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	store.Apply([]market.PricePoint{
		pointAt("BTCUSDT", t0.Add(2*time.Minute), "102"),
		pointAt("BTCUSDT", t0, "100"),
		pointAt("BTCUSDT", t0.Add(time.Minute), "101"),
	})
	store.Apply([]market.PricePoint{pointAt("BTCUSDT", t0.Add(time.Minute), "101.5")})

	series := store.Series("BTCUSDT")
	if len(series) != 3 {
		t.Fatalf("len(series) = %d", len(series))
	}
	if !series[1].Price.Equal(decimal.RequireFromString("101.5")) {
		t.Fatalf("upserted price = %s", series[1].Price)
	}

	store.Apply([]market.PricePoint{pointAt("BTCUSDT", t0.Add(3*time.Minute), "103")})
	store.Apply([]market.PricePoint{pointAt("BTCUSDT", t0.Add(-time.Minute), "99")})
	series = store.Series("BTCUSDT")
	if len(series) != 3 || !series[0].ObservedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("pruned series = %#v", series)
	}
	store.Prune(t0.Add(4 * time.Minute))
	if store.Symbols() != 0 {
		t.Fatalf("symbols = %d", store.Symbols())
	}
}

func pointAt(symbol string, observedAt time.Time, price string) market.PricePoint {
	return market.PricePoint{
		Symbol:     symbol,
		ObservedAt: observedAt,
		Price:      decimal.RequireFromString(price),
	}
}
