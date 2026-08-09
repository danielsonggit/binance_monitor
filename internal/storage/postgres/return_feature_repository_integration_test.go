package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/feature"
	"binance-monitor/internal/universe"
)

type integrationFeatureClock struct{ now time.Time }

func (c integrationFeatureClock) Now() time.Time { return c.now }

func TestReturnFeatureRepositoryLoadCalculateSaveAndReplayIntegration(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_TEST_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_TEST_URL is not set")
	}
	if !strings.Contains(strings.ToLower(databaseURL), "test") {
		t.Fatal("POSTGRES_TEST_URL must point to a database whose name contains test")
	}
	ctx := context.Background()
	pool, err := Open(ctx, databaseURL, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		TRUNCATE return_feature_snapshots, collection_runs, system_heartbeats,
			market_snapshots_5m, klines_15m, instruments
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}

	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if _, err := NewUniverseRepository(pool).Reconcile(ctx, universe.Snapshot{
		ObservedAt: asOf.Add(-25 * time.Hour),
		Instruments: []market.Instrument{
			testInstrument("BTCUSDT"),
			testInstrument("ETHUSDT"),
		},
		MissingConfirmations: 2,
	}); err != nil {
		t.Fatal(err)
	}
	klines := make([]market.Kline, 0, 97)
	for closeAt := asOf.Add(-24 * time.Hour); !closeAt.After(asOf); closeAt = closeAt.Add(15 * time.Minute) {
		klines = append(klines, integrationKline(closeAt.Add(-15*time.Minute)))
	}
	if _, err := NewKlineRepository(pool).Save(ctx, market.KlineBatch{
		Items: klines, Source: market.KlineSourceBinanceFutures, ReceivedAt: asOf,
	}); err != nil {
		t.Fatal(err)
	}

	calculator, err := feature.NewCalculator(feature.Policy{
		CurrentMaxAge: 5 * time.Minute, BaselineMaxOffset: 5 * time.Minute,
		MinimumQuality: 75, LiquidityLookback: time.Hour,
		FeatureVersion: market.ReturnFeatureVersion1,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewReturnFeatureRepository(pool)
	service, err := feature.NewService(repository, calculator, integrationFeatureClock{now: asOf}, 25*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Symbols != 2 || result.ValidMetrics != 4 || result.InvalidMetrics != 4 ||
		result.InvalidReasons[feature.InvalidCurrentMissing] != 4 || result.Written != 2 {
		t.Fatalf("result = %#v", result)
	}

	var rows int
	var valid15m bool
	var return15m string
	var invalid24h string
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM return_feature_snapshots),
			b.is_valid_15m,
			b.return_15m::text,
			e.quality_json->'24h'->>'invalid_reason'
		FROM return_feature_snapshots b
		JOIN instruments bi ON bi.id = b.instrument_id AND bi.symbol = 'BTCUSDT'
		JOIN return_feature_snapshots e ON e.as_of = b.as_of AND e.feature_version = b.feature_version
		JOIN instruments ei ON ei.id = e.instrument_id AND ei.symbol = 'ETHUSDT'
		LIMIT 1`).Scan(&rows, &valid15m, &return15m, &invalid24h); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || !valid15m || return15m != "0.000000000000" || invalid24h != feature.InvalidCurrentMissing {
		t.Fatalf("rows=%d valid=%v return=%s invalid=%s", rows, valid15m, return15m, invalid24h)
	}

	repeat, err := service.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Written != 2 {
		t.Fatalf("repeat = %#v", repeat)
	}
	var featureRows int
	var runRows int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM return_feature_snapshots),
			(SELECT count(*) FROM collection_runs WHERE job_type = 'RETURN_FEATURES_5M')`).Scan(&featureRows, &runRows); err != nil {
		t.Fatal(err)
	}
	if featureRows != 2 || runRows != 1 {
		t.Fatalf("feature rows=%d run rows=%d", featureRows, runRows)
	}
}
