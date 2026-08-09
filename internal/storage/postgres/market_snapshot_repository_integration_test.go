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

func TestMarketSnapshotRepositoryIntegration(t *testing.T) {
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

	t0 := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	_, err = NewUniverseRepository(pool).Reconcile(ctx, universe.Snapshot{
		ObservedAt: t0,
		Instruments: []market.Instrument{
			testInstrument("BTCUSDT"),
			testInstrument("ETHUSDT"),
		},
		MissingConfirmations: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	repository := NewMarketSnapshotRepository(pool)
	first, err := repository.Save(ctx, market.SnapshotBatch{
		BucketStart: t0,
		BucketEnd:   t0.Add(market.SnapshotInterval),
		Items: []market.SnapshotItem{
			{Ticker: snapshotTicker("BTCUSDT", t0.Add(5*time.Minute-time.Second)), QualityScore: 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Expected != 2 || first.Actual != 1 || first.Missing != 1 || first.Status != "DEGRADED" {
		t.Fatalf("first result = %#v", first)
	}
	if len(first.MissingSymbols) != 1 || first.MissingSymbols[0] != "ETHUSDT" {
		t.Fatalf("missing symbols = %#v", first.MissingSymbols)
	}

	duplicate, err := repository.Save(ctx, market.SnapshotBatch{
		BucketStart: t0,
		BucketEnd:   t0.Add(market.SnapshotInterval),
		Items:       []market.SnapshotItem{{Ticker: snapshotTicker("BTCUSDT", t0.Add(4*time.Minute)), QualityScore: 90}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.AlreadyApplied || duplicate.Actual != 1 {
		t.Fatalf("duplicate result = %#v", duplicate)
	}

	secondStart := t0.Add(market.SnapshotInterval)
	second, err := repository.Save(ctx, market.SnapshotBatch{
		BucketStart: secondStart,
		BucketEnd:   secondStart.Add(market.SnapshotInterval),
		Items: []market.SnapshotItem{
			{Ticker: snapshotTicker("BTCUSDT", secondStart.Add(5*time.Minute-time.Second)), QualityScore: 100},
			{Ticker: snapshotTicker("ETHUSDT", secondStart.Add(5*time.Minute-time.Second)), QualityScore: 100},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "SUCCEEDED" || second.Actual != 2 {
		t.Fatalf("second result = %#v", second)
	}

	gaps, err := repository.MarkGaps(ctx, t0.Add(20*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if gaps != 2 {
		t.Fatalf("gaps = %d", gaps)
	}
	points, err := repository.LoadRecent(ctx, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 {
		t.Fatalf("prewarm points = %d", len(points))
	}

	var snapshotRows, degradedRuns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM market_snapshots_5m`).Scan(&snapshotRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM collection_runs
		WHERE job_type = $1 AND status = 'DEGRADED'`, market.SnapshotJobType).Scan(&degradedRuns); err != nil {
		t.Fatal(err)
	}
	if snapshotRows != 3 || degradedRuns != 3 {
		t.Fatalf("snapshot rows=%d degraded runs=%d", snapshotRows, degradedRuns)
	}
}

func snapshotTicker(symbol string, eventTime time.Time) market.MiniTicker {
	return market.MiniTicker{
		Symbol:         symbol,
		EventTime:      eventTime,
		ReceivedAt:     eventTime.Add(10 * time.Millisecond),
		LastPrice:      decimal.RequireFromString("101.25"),
		OpenPrice24h:   decimal.RequireFromString("100"),
		HighPrice24h:   decimal.RequireFromString("102"),
		LowPrice24h:    decimal.RequireFromString("99"),
		BaseVolume24h:  decimal.RequireFromString("123.45"),
		QuoteVolume24h: decimal.RequireFromString("12499.3125"),
	}
}
