package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/ranking"
	"binance-monitor/internal/universe"
)

func TestRankingRepositoryCalculateSaveAndReplayIntegration(t *testing.T) {
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
		TRUNCATE ranking_snapshot_items, ranking_snapshots, return_feature_snapshots,
			collection_runs, system_heartbeats, market_snapshots_5m, klines_15m, instruments
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}

	asOf := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	instruments := []market.Instrument{
		testInstrument("BTCUSDT"), testInstrument("ETHUSDT"), testInstrument("SOLUSDT"),
		testInstrument("XAUUSDT"), testInstrument("SILVERUSDT"),
	}
	if _, err := NewUniverseRepository(pool).Reconcile(ctx, universe.Snapshot{
		ObservedAt: asOf.Add(-25 * time.Hour), Instruments: instruments, MissingConfirmations: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE instruments SET sector = 'TRADFI' WHERE symbol IN ('XAUUSDT', 'SILVERUSDT')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_feature_snapshots (
			instrument_id, as_of, feature_version,
			current_price, current_price_at, current_source, current_age_seconds,
			recent_quote_volume_1h, quote_volume_24h,
			return_15m, return_1h, return_4h, return_24h,
			is_valid_15m, is_valid_1h, is_valid_4h, is_valid_24h,
			quality_json, calculated_at
		)
		SELECT
			i.id, $1, $2,
			10, $1, 'KLINE_15M', 0,
			100,
			CASE i.symbol WHEN 'ETHUSDT' THEN 200 WHEN 'BTCUSDT' THEN 100 ELSE 50 END,
			CASE i.symbol WHEN 'BTCUSDT' THEN 2 WHEN 'ETHUSDT' THEN 2 WHEN 'SOLUSDT' THEN 1 ELSE 0.5 END,
			CASE i.symbol WHEN 'BTCUSDT' THEN 2 WHEN 'ETHUSDT' THEN 2 WHEN 'SOLUSDT' THEN 1 ELSE 0.5 END,
			CASE i.symbol WHEN 'BTCUSDT' THEN 2 WHEN 'ETHUSDT' THEN 2 WHEN 'SOLUSDT' THEN 1 ELSE 0.5 END,
			CASE i.symbol WHEN 'BTCUSDT' THEN 2 WHEN 'ETHUSDT' THEN 2 WHEN 'SOLUSDT' THEN 1 ELSE 0.5 END,
			true, true, true, true,
			'{}'::jsonb, $1
		FROM instruments i
		WHERE i.symbol <> 'SILVERUSDT'`, asOf, market.ReturnFeatureVersion1); err != nil {
		t.Fatal(err)
	}

	calculator, err := ranking.NewCalculator(ranking.Policy{
		RankingVersion: market.RankingVersion1,
		FeatureVersion: market.ReturnFeatureVersion1,
		TopN:           2,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository := NewRankingRepository(pool)
	service, err := ranking.NewService(repository, calculator, integrationFeatureClock{now: asOf.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunAt(ctx, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if result.Groups != 8 || result.Items != 12 || result.ActiveMetrics != 20 || result.Eligible != 16 || result.Positive != 16 || result.Written != 12 {
		t.Fatalf("result=%#v", result)
	}

	rows, err := pool.Query(ctx, `
		SELECT i.symbol
		FROM ranking_snapshots s
		JOIN ranking_snapshot_items ri ON ri.ranking_snapshot_id = s.id AND ri.as_of = s.as_of
		JOIN instruments i ON i.id = ri.instrument_id
		WHERE s.as_of = $1 AND s.sector = 'CRYPTO' AND s.horizon = '15m'
		ORDER BY ri.rank_position`, asOf)
	if err != nil {
		t.Fatal(err)
	}
	var symbols []string
	for rows.Next() {
		var symbol string
		if err := rows.Scan(&symbol); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		symbols = append(symbols, symbol)
	}
	rows.Close()
	if strings.Join(symbols, ",") != "ETHUSDT,BTCUSDT" {
		t.Fatalf("symbols=%v", symbols)
	}

	repeat, err := service.RunAt(ctx, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Written != 12 {
		t.Fatalf("repeat=%#v", repeat)
	}
	var snapshots, items, runs int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM ranking_snapshots),
			(SELECT count(*) FROM ranking_snapshot_items),
			(SELECT count(*) FROM collection_runs WHERE job_type = 'RANKINGS_5M')`).
		Scan(&snapshots, &items, &runs); err != nil {
		t.Fatal(err)
	}
	if snapshots != 8 || items != 12 || runs != 1 {
		t.Fatalf("snapshots=%d items=%d runs=%d", snapshots, items, runs)
	}
}
