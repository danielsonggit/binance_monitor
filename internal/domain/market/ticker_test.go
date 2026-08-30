package market

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestMiniTickerValidate(t *testing.T) {
	now := time.Now()
	ticker := MiniTicker{
		Symbol:         "BTCUSDT",
		EventTime:      now,
		ReceivedAt:     now,
		LastPrice:      decimal.RequireFromString("65000.1"),
		OpenPrice24h:   decimal.RequireFromString("64000"),
		HighPrice24h:   decimal.RequireFromString("66000"),
		LowPrice24h:    decimal.RequireFromString("63000"),
		BaseVolume24h:  decimal.RequireFromString("100"),
		QuoteVolume24h: decimal.RequireFromString("6500000"),
	}
	if err := ticker.Validate(); err != nil {
		t.Fatal(err)
	}
	ticker.LastPrice = decimal.Zero
	if err := ticker.Validate(); err == nil {
		t.Fatal("expected price validation error")
	}
}

func TestMiniTickerPriceChangePercent24h(t *testing.T) {
	now := time.Now()
	ticker := MiniTicker{
		Symbol:         "BTCUSDT",
		EventTime:      now,
		ReceivedAt:     now,
		LastPrice:      decimal.RequireFromString("102.5"),
		OpenPrice24h:   decimal.RequireFromString("100"),
		HighPrice24h:   decimal.RequireFromString("103"),
		LowPrice24h:    decimal.RequireFromString("99"),
		BaseVolume24h:  decimal.NewFromInt(1),
		QuoteVolume24h: decimal.NewFromInt(100),
	}
	if !ticker.PriceChangePercent24h().Equal(decimal.RequireFromString("2.5")) {
		t.Fatalf("change = %s", ticker.PriceChangePercent24h())
	}
}
