package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/universe"
)

func TestUniverseRepositoryLifecycleIntegration(t *testing.T) {
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

	repository := NewUniverseRepository(pool)
	t0 := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	initial := universe.Snapshot{
		ObservedAt:           t0,
		Instruments:          []market.Instrument{testInstrument("BTCUSDT"), testInstrument("ETHUSDT")},
		MissingConfirmations: 2,
	}
	result, err := repository.Reconcile(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 2 || result.ActiveBefore != 0 {
		t.Fatalf("initial result = %#v", result)
	}

	duplicate, err := repository.Reconcile(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.AlreadyApplied {
		t.Fatalf("duplicate result = %#v", duplicate)
	}

	firstMissing := initial
	firstMissing.ObservedAt = t0.Add(time.Hour)
	firstMissing.Instruments = []market.Instrument{testInstrument("BTCUSDT")}
	result, err = repository.Reconcile(ctx, firstMissing)
	if err != nil {
		t.Fatal(err)
	}
	if result.MissingPending != 1 || result.Closed != 0 {
		t.Fatalf("first missing result = %#v", result)
	}

	secondMissing := firstMissing
	secondMissing.ObservedAt = t0.Add(2 * time.Hour)
	result, err = repository.Reconcile(ctx, secondMissing)
	if err != nil {
		t.Fatal(err)
	}
	if result.Closed != 1 {
		t.Fatalf("second missing result = %#v", result)
	}

	relisted := initial
	relisted.ObservedAt = t0.Add(3 * time.Hour)
	result, err = repository.Reconcile(ctx, relisted)
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 1 {
		t.Fatalf("relisted result = %#v", result)
	}

	var totalETH, activeETH int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE valid_to IS NULL)
		FROM instruments
		WHERE symbol = 'ETHUSDT'`).Scan(&totalETH, &activeETH); err != nil {
		t.Fatal(err)
	}
	if totalETH != 2 || activeETH != 1 {
		t.Fatalf("ETH lifetimes total=%d active=%d", totalETH, activeETH)
	}

	statusChanged := relisted
	statusChanged.ObservedAt = t0.Add(4 * time.Hour)
	statusChanged.Instruments = []market.Instrument{pendingInstrument("BTCUSDT"), testInstrument("ETHUSDT")}
	result, err = repository.Reconcile(ctx, statusChanged)
	if err != nil {
		t.Fatal(err)
	}
	if result.Closed != 1 || result.Inserted != 1 {
		t.Fatalf("status change result = %#v", result)
	}
	var currentStatus market.ExchangeStatus
	var btcLifetimes int
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(exchange_status) FILTER (WHERE valid_to IS NULL)
		FROM instruments
		WHERE symbol = 'BTCUSDT'`).Scan(&btcLifetimes, &currentStatus); err != nil {
		t.Fatal(err)
	}
	if btcLifetimes != 2 || currentStatus != market.ExchangeStatusPendingTrading {
		t.Fatalf("BTC lifetimes=%d current exchange status=%s", btcLifetimes, currentStatus)
	}
}

func testInstrument(symbol string) market.Instrument {
	return market.Instrument{
		Symbol:            symbol,
		BaseAsset:         strings.TrimSuffix(symbol, "USDT"),
		QuoteAsset:        "USDT",
		Sector:            market.SectorCrypto,
		ContractType:      "PERPETUAL",
		PricePrecision:    2,
		QuantityPrecision: 3,
		UnderlyingType:    "COIN",
	}
}
