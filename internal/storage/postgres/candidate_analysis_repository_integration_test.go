package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

func TestCandidateAnalysisRepositoryReadsOnlyValidClosedDataIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE instruments RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 8, 28, 8, 0, 0, 0, time.UTC)
	var instrumentID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO instruments (
			symbol, base_asset, quote_asset, sector, contract_type, status,
			valid_from, last_seen_at, exchange_status
		) VALUES ('BTCUSDT', 'BTC', 'USDT', 'CRYPTO', 'PERPETUAL', 'ACTIVE', $1, $1, 'TRADING')
		RETURNING id`, asOf.Add(-24*time.Hour)).Scan(&instrumentID); err != nil {
		t.Fatal(err)
	}
	for offset := -15; offset <= 0; offset += 5 {
		point := asOf.Add(time.Duration(offset) * time.Minute)
		if _, err := pool.Exec(ctx, `
			INSERT INTO return_feature_snapshots (
				instrument_id, as_of, feature_version,
				current_price, current_price_at, current_source, current_age_seconds,
				recent_quote_volume_1h, quote_volume_24h,
				return_15m, return_1h, return_4h, return_24h,
				is_valid_15m, is_valid_1h, is_valid_4h, is_valid_24h,
				quality_json, calculated_at
			) VALUES ($1, $2, $3, 100, $2, 'SNAPSHOT_5M', 0, 1000, 24000,
				$4, $4, $4, $4, true, true, true, true, '{}'::jsonb, $2)`,
			instrumentID, point, market.ReturnFeatureVersion1, float64(offset+20)); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 21; index++ {
		closeAt := asOf.Add(time.Duration(index-20) * 15 * time.Minute)
		openAt := closeAt.Add(-15 * time.Minute)
		if _, err := pool.Exec(ctx, `
			INSERT INTO klines_15m (
				instrument_id, open_time, close_time, open, high, low, close,
				volume, quote_volume, trade_count, taker_buy_base_volume,
				taker_buy_quote_volume, source, received_at
			) VALUES ($1, $2, $3::timestamptz - interval '1 millisecond', 100, 110, 90, 101,
				1, 1000, 10, 0.5, 500, 'BINANCE_FUTURES', $3)`, instrumentID, openAt, closeAt); err != nil {
			t.Fatal(err)
		}
	}
	repository := NewCandidateAnalysisRepository(pool)
	latest, err := repository.LatestFeatureAsOf(ctx, market.ReturnFeatureVersion1)
	if err != nil || !latest.Equal(asOf) {
		t.Fatalf("latest=%s err=%v", latest, err)
	}
	features, err := repository.FeatureObservations(ctx, asOf.Add(-5*time.Minute), asOf, market.ReturnFeatureVersion1)
	if err != nil {
		t.Fatal(err)
	}
	if len(features) != 2 || features[1].PreviousReturn15m == nil || !features[1].Previous15mAsOf.Equal(asOf.Add(-15*time.Minute)) {
		t.Fatalf("features=%#v", features)
	}
	klines, err := repository.KlineObservations(ctx, asOf.Add(-6*time.Hour), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(klines) != 21 || !klines[len(klines)-1].ClosedAt.Equal(asOf) {
		t.Fatalf("klines=%d last=%s", len(klines), klines[len(klines)-1].ClosedAt)
	}
}
