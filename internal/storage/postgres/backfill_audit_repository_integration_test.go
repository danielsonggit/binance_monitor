package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/backfill"
)

func TestBackfillAuditRepositoryUpsertsWindowIntegration(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `DELETE FROM collection_runs WHERE job_type = 'KLINE_15M_BACKFILL'`); err != nil {
		t.Fatal(err)
	}

	start := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	result := backfill.Result{
		WindowStart: start, WindowEnd: start.Add(30 * time.Hour), Concurrency: 8,
		Symbols: 1, Expected: 120, Written: 100, Remaining: 20,
		PlannedGaps:   []backfill.Gap{{Symbol: "BTCUSDT", Start: start, End: start.Add(30 * time.Hour), Count: 120}},
		RemainingGaps: []backfill.Gap{{Symbol: "BTCUSDT", Start: start, End: start.Add(5 * time.Hour), Count: 20}},
	}
	repository := NewBackfillAuditRepository(pool)
	if err := repository.Record(ctx, result, start, start.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	result.Remaining = 0
	result.RemainingGaps = nil
	if err := repository.Record(ctx, result, start.Add(2*time.Minute), start.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	var count int
	var status string
	var missing int
	var concurrency int
	var gapStatus string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(status), min(missing_count), max((metadata->>'concurrency')::int),
			max(metadata->'gaps'->0->>'status')
		FROM collection_runs
		WHERE job_type = 'KLINE_15M_BACKFILL'`).Scan(&count, &status, &missing, &concurrency, &gapStatus); err != nil {
		t.Fatal(err)
	}
	if count != 2 || status != "SUCCEEDED" || missing != 0 || concurrency != 8 || gapStatus != "RECOVERED" {
		t.Fatalf("count=%d status=%s missing=%d concurrency=%d gap=%s", count, status, missing, concurrency, gapStatus)
	}
}
