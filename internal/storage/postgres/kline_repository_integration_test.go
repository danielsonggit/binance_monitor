package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/universe"
	"github.com/shopspring/decimal"
)

func TestKlineRepositoryIdempotentUpsertIntegration(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_TEST_URL is not set")
	}
	if !strings.Contains(strings.ToLower(databaseURL), "test") {
		t.Fatal("POSTGRES_TEST_URL must point to a database whose name contains test")
	}

	ctx := context.Background()
	pool, err := Open(ctx, databaseURL, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		TRUNCATE collection_runs, system_heartbeats,
			market_snapshots_5m, klines_15m, instruments
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}

	observedAt := time.Date(2026, 8, 9, 2, 0, 0, 0, time.UTC)
	if _, err := NewUniverseRepository(pool).Reconcile(ctx, universe.Snapshot{
		ObservedAt:           observedAt,
		Instruments:          []market.Instrument{testInstrument("BTCUSDT")},
		MissingConfirmations: 2,
	}); err != nil {
		t.Fatal(err)
	}

	first := integrationKline(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	second := integrationKline(first.OpenTime.Add(15 * time.Minute))
	repository := NewKlineRepository(pool)
	batch := market.KlineBatch{
		Items:      []market.Kline{first, second},
		Source:     market.KlineSourceBinanceFutures,
		ReceivedAt: observedAt,
	}
	result, err := repository.Save(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempted != 2 || result.Upserted != 2 {
		t.Fatalf("first result = %#v", result)
	}

	batch.Items[0].Close = decimal.RequireFromString("106.25")
	batch.Items[0].High = decimal.RequireFromString("111")
	batch.ReceivedAt = observedAt.Add(time.Minute)
	result, err = repository.Save(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Attempted != 2 || result.Upserted != 2 {
		t.Fatalf("duplicate result = %#v", result)
	}

	var rows int
	var closePrice string
	var receivedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM klines_15m), close::text, received_at
		FROM klines_15m
		WHERE open_time = $1`, first.OpenTime).Scan(&rows, &closePrice, &receivedAt); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || closePrice != "106.250000000000000000" || !receivedAt.Equal(batch.ReceivedAt) {
		t.Fatalf("rows=%d close=%s received_at=%s", rows, closePrice, receivedAt)
	}

	incomplete := integrationKline(observedAt)
	if _, err := repository.Save(ctx, market.KlineBatch{
		Items:      []market.Kline{incomplete},
		Source:     market.KlineSourceBinanceFutures,
		ReceivedAt: incomplete.CloseTime.Add(-time.Millisecond),
	}); err == nil || !strings.Contains(err.Error(), "尚未完成") {
		t.Fatalf("incomplete error = %v", err)
	}

	missing := integrationKline(first.OpenTime.Add(30 * time.Minute))
	missing.Symbol = "UNKNOWNUSDT"
	if _, err := repository.Save(ctx, market.KlineBatch{
		Items:      []market.Kline{missing},
		Source:     market.KlineSourceBinanceFutures,
		ReceivedAt: observedAt,
	}); err == nil || !strings.Contains(err.Error(), "UNKNOWNUSDT") {
		t.Fatalf("missing instrument error = %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM klines_15m`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("rows after rejected writes = %d", rows)
	}
}

func integrationKline(openTime time.Time) market.Kline {
	return market.Kline{
		Symbol:              "BTCUSDT",
		Interval:            market.KlineInterval15m,
		OpenTime:            openTime,
		CloseTime:           openTime.Add(15*time.Minute - time.Millisecond),
		Open:                decimal.RequireFromString("100"),
		High:                decimal.RequireFromString("110"),
		Low:                 decimal.RequireFromString("95"),
		Close:               decimal.RequireFromString("105"),
		Volume:              decimal.RequireFromString("12.5"),
		QuoteVolume:         decimal.RequireFromString("1300"),
		TradeCount:          42,
		TakerBuyBaseVolume:  decimal.RequireFromString("7"),
		TakerBuyQuoteVolume: decimal.RequireFromString("730"),
	}
}
