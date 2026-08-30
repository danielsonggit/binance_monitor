package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/notification"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMarketQueryRepositoryIntegration(t *testing.T) {
	ctx, pool := openMHR6IntegrationPool(t)
	asOf := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	var btcID, xauID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO instruments (
			symbol, base_asset, quote_asset, sector, contract_type, status,
			valid_from, last_seen_at
		) VALUES ('BTCUSDT', 'BTC', 'USDT', 'CRYPTO', 'PERPETUAL', 'TRADING', $1, $1)
		RETURNING id`, asOf.Add(-48*time.Hour)).Scan(&btcID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO instruments (
			symbol, base_asset, quote_asset, sector, contract_type, status,
			valid_from, last_seen_at
		) VALUES ('XAUUSDT', 'XAU', 'USDT', 'TRADFI', 'PERPETUAL', 'TRADING', $1, $1)
		RETURNING id`, asOf.Add(-48*time.Hour)).Scan(&xauID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_feature_snapshots (
			instrument_id, as_of, feature_version, current_price, current_price_at,
			current_source, current_age_seconds, recent_quote_volume_1h, quote_volume_24h,
			return_15m, return_1h, return_4h, return_24h,
			is_valid_15m, is_valid_1h, is_valid_4h, is_valid_24h,
			quality_json, calculated_at
		) VALUES
			($1, $3::timestamptz, $4::text, 65000, $3::timestamptz, 'KLINE_15M', 0, 1000000, 24000000,
			 1.1, 2.2, 3.3, 4.4, true, true, true, true, '{}'::jsonb, $3::timestamptz + interval '1 second'),
			($2, $3::timestamptz, $4::text, 2400, $3::timestamptz, 'KLINE_15M', 0, 500000, 12000000,
			 NULL, 0.5, 1.5, 2.5, false, true, true, true,
			 '{"15m":{"invalid_reason":"missing_baseline"}}'::jsonb, $3::timestamptz + interval '1 second')`,
		btcID, xauID, asOf, market.ReturnFeatureVersion1); err != nil {
		t.Fatal(err)
	}
	var rankingID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO ranking_snapshots (
			as_of, ranking_version, feature_version, sector, horizon, requested_limit,
			active_count, eligible_count, positive_count, ranked_count, calculated_at
		) VALUES ($1::timestamptz, $2::text, $3::text, 'CRYPTO', '1h', 5, 1, 1, 1, 1,
			$1::timestamptz + interval '2 seconds')
		RETURNING id`, asOf, market.RankingVersion1, market.ReturnFeatureVersion1).Scan(&rankingID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO ranking_snapshot_items (
			ranking_snapshot_id, as_of, rank_position, instrument_id,
			return_percent, current_price, quote_volume_24h, percentile
		) VALUES ($1, $2, 1, $3, 2.2, 65000, 24000000, 100)`, rankingID, asOf, btcID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO collection_runs (
			idempotency_key, job_type, window_start, window_end, expected_count,
			actual_count, missing_count, status, completed_at, metadata
		) VALUES
			('features-quality', 'RETURN_FEATURES_5M', $1::timestamptz - interval '5 minutes', $1::timestamptz,
			 8, 7, 1, 'DEGRADED', $1::timestamptz + interval '2 seconds',
			 '{"invalid_reasons":{"missing_baseline":1}}'::jsonb),
			('backfill-quality', 'KLINE_15M_BACKFILL', $1::timestamptz - interval '30 hours', $1::timestamptz,
			 192, 192, 0, 'SUCCEEDED', $1::timestamptz + interval '1 second', '{}'::jsonb),
			('snapshot-quality', 'MARKET_SNAPSHOT_5M', $1::timestamptz - interval '5 minutes', $1::timestamptz,
			 2, 1, 1, 'SUCCEEDED', $1::timestamptz + interval '1 second',
			 '{"coverage":{"rule_version":"binance-usdm-availability-v1","raw_expected":2,"raw_actual":1,"raw_missing":1,"adjusted_expected":1,"adjusted_actual":1,"adjusted_missing":0,"state_counts":{"OPEN":1,"MARKET_CLOSED":1}},"state_symbols":{"OPEN":["BTCUSDT"],"MARKET_CLOSED":["XAUUSDT"]}}'::jsonb)`, asOf); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO system_heartbeats (component, observed_at, status, detail_json)
		VALUES ('v2-worker', $1, 'HEALTHY', '{"phase":"idle"}'::jsonb)`, asOf.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	repository := NewMarketQueryRepository(pool)
	ranking, err := repository.LatestRanking(ctx, market.SectorCrypto, market.ReturnHorizon1h, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ranking.Items) != 1 || ranking.Items[0].Symbol != "BTCUSDT" ||
		ranking.Items[0].Returns[market.ReturnHorizon24h].ReturnPercent.String() != "4.4" {
		t.Fatalf("ranking=%#v", ranking)
	}
	feature, err := repository.LatestFeature(ctx, "XAUUSDT")
	if err != nil {
		t.Fatal(err)
	}
	if feature.CurrentPrice == nil || feature.CurrentPrice.String() != "2400" ||
		feature.Returns[market.ReturnHorizon15m].IsValid ||
		feature.Returns[market.ReturnHorizon15m].InvalidReason != "missing_baseline" {
		t.Fatalf("feature=%#v", feature)
	}
	quality, err := repository.LatestQuality(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if quality.ActiveSymbols != 2 || quality.FeatureRows != 2 || quality.ActiveMetrics != 8 ||
		quality.ValidMetrics != 7 || quality.InvalidMetrics != 1 || quality.CoveragePercent.String() != "87.5" ||
		quality.InvalidReasons["missing_baseline"] != 1 || quality.Backfill == nil || quality.Worker == nil {
		t.Fatalf("quality=%#v", quality)
	}
	if quality.Snapshot == nil || quality.Snapshot.Coverage.RawExpected != 2 ||
		quality.Snapshot.Coverage.AdjustedExpected != 1 ||
		quality.Snapshot.Coverage.OperationalExpected != 1 ||
		quality.Snapshot.Coverage.OperationalActual != 1 ||
		quality.Snapshot.StateSymbols[market.AvailabilityMarketClosed][0] != "XAUUSDT" {
		t.Fatalf("snapshot quality=%#v", quality.Snapshot)
	}
}

func TestNotificationRepositoryIdempotencyDeliveryAndRecoveryIntegration(t *testing.T) {
	ctx, pool := openMHR6IntegrationPool(t)
	repository := NewNotificationRepository(pool)
	enqueuer, err := notification.NewEnqueuer(repository, []string{"-1001", "-1002"})
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	request := notification.EnqueueRequest{
		IdempotencyKey: "scheduled-market-report:2026-08-10T04:00:00Z",
		ScheduledFor:   slot, DataAsOf: slot, Messages: []string{"part-1", "part-2"}, MaxAttempts: 3,
	}
	created, err := enqueuer.Enqueue(ctx, request)
	if err != nil || !created.Created {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	replayed, err := enqueuer.Enqueue(ctx, request)
	if err != nil || replayed.Created || replayed.OutboxID != created.OutboxID {
		t.Fatalf("replayed=%#v err=%v", replayed, err)
	}
	changedRecipients, err := notification.NewEnqueuer(repository, []string{"-9999"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := changedRecipients.Enqueue(ctx, request); err == nil || !strings.Contains(err.Error(), "内容冲突") {
		t.Fatalf("expected recipient conflict, error=%v", err)
	}
	claimAt := time.Now().UTC().Add(time.Minute)
	job, found, err := repository.ClaimDue(ctx, claimAt)
	if err != nil || !found || job.Attempt != 1 || len(job.Deliveries) != 4 {
		t.Fatalf("job=%#v found=%v err=%v", job, found, err)
	}
	for index, delivery := range job.Deliveries {
		if err := repository.MarkSending(ctx, job.OutboxID, delivery.ChatID, delivery.PartIndex); err != nil {
			t.Fatal(err)
		}
		if err := repository.MarkSent(ctx, job.OutboxID, delivery.ChatID, delivery.PartIndex, int64(100+index)); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Complete(ctx, job.OutboxID, slot.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var status string
	var deliveries int
	if err := pool.QueryRow(ctx, `
		SELECT o.status, count(d.*)
		FROM notification_outbox o
		JOIN notification_deliveries d ON d.outbox_id = o.id
		WHERE o.id = $1 GROUP BY o.status`, job.OutboxID).Scan(&status, &deliveries); err != nil {
		t.Fatal(err)
	}
	if status != "SENT" || deliveries != 4 {
		t.Fatalf("status=%s deliveries=%d", status, deliveries)
	}
	if _, found, err := repository.ClaimDue(ctx, claimAt.Add(time.Minute)); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}

	staleRequest := request
	staleRequest.IdempotencyKey = "scheduled-market-report:stale"
	staleRequest.ScheduledFor = slot.Add(time.Minute)
	staleRequest.DataAsOf = staleRequest.ScheduledFor
	stale, err := enqueuer.Enqueue(ctx, staleRequest)
	if err != nil {
		t.Fatal(err)
	}
	staleClaimAt := claimAt.Add(2 * time.Minute)
	staleJob, found, err := repository.ClaimDue(ctx, staleClaimAt)
	if err != nil || !found {
		t.Fatalf("job=%#v found=%v err=%v", staleJob, found, err)
	}
	first := staleJob.Deliveries[0]
	if err := repository.MarkSending(ctx, stale.OutboxID, first.ChatID, first.PartIndex); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecoverStale(ctx, staleClaimAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM notification_outbox WHERE id = $1`, stale.OutboxID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "UNKNOWN" {
		t.Fatalf("stale status=%s", status)
	}
}

func openMHR6IntegrationPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
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
	t.Cleanup(pool.Close)
	if _, err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		TRUNCATE notification_deliveries, notification_outbox,
			ranking_snapshot_items, ranking_snapshots, return_feature_snapshots,
			collection_runs, system_heartbeats, market_snapshots_5m, klines_15m, instruments
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}
