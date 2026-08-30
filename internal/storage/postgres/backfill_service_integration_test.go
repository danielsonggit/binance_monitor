package postgres

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"binance-monitor/internal/backfill"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/universe"
)

type integrationBackfillClock struct{ now time.Time }

func (c integrationBackfillClock) Now() time.Time { return c.now }

type integrationArchiveSource struct{ calls atomic.Int32 }

func (s *integrationArchiveSource) FetchDailyKlines(
	_ context.Context,
	symbol string,
	_ market.KlineInterval,
	day time.Time,
) ([]market.Kline, error) {
	s.calls.Add(1)
	return integrationKlineRange(symbol, day, day.Add(24*time.Hour)), nil
}

type integrationRESTSource struct{ calls atomic.Int32 }

func (s *integrationRESTSource) FetchKlines(
	_ context.Context,
	query market.KlineQuery,
) ([]market.Kline, error) {
	s.calls.Add(1)
	return integrationKlineRange(query.Symbol, query.StartTime, query.EndTime.Add(time.Millisecond)), nil
}

func TestBackfillServiceEmptyPartialMiddleGapAndResumeIntegration(t *testing.T) {
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
		TRUNCATE collection_runs, system_heartbeats,
			market_snapshots_5m, klines_15m, instruments
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if _, err := NewUniverseRepository(pool).Reconcile(ctx, universe.Snapshot{
		ObservedAt: now,
		Instruments: []market.Instrument{
			testInstrument("BTCUSDT"),
			testInstrument("ETHUSDT"),
		},
		MissingConfirmations: 2,
	}); err != nil {
		t.Fatal(err)
	}

	windowStart := now.Add(-30 * time.Hour)
	middleGap := windowStart.Add(8 * time.Hour)
	partial := integrationKlineRange("BTCUSDT", windowStart, now)
	withoutMiddle := partial[:0]
	for _, item := range partial {
		if !item.OpenTime.Equal(middleGap) {
			withoutMiddle = append(withoutMiddle, item)
		}
	}
	klineRepository := NewKlineRepository(pool)
	if _, err := klineRepository.Save(ctx, market.KlineBatch{
		Items: withoutMiddle, Source: market.KlineSourceBinanceFutures, ReceivedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	archive := &integrationArchiveSource{}
	rest := &integrationRESTSource{}
	service, err := backfill.NewService(klineRepository, archive, rest, integrationBackfillClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(ctx, 30*time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expected != 240 || result.PresentBefore != 119 || result.Remaining != 0 ||
		archive.calls.Load() != 2 || rest.calls.Load() != 1 {
		t.Fatalf("result=%#v archive=%d rest=%d", result, archive.calls.Load(), rest.calls.Load())
	}

	archiveBefore := archive.calls.Load()
	restBefore := rest.calls.Load()
	repeat, err := service.Run(ctx, 30*time.Hour, 2)
	if err != nil {
		t.Fatal(err)
	}
	if repeat.PresentBefore != 240 || repeat.Written != 0 || repeat.Remaining != 0 ||
		archive.calls.Load() != archiveBefore || rest.calls.Load() != restBefore {
		t.Fatalf("repeat=%#v archive=%d rest=%d", repeat, archive.calls.Load(), rest.calls.Load())
	}
	coverage, err := klineRepository.ExistingOpenTimes(ctx, []string{"BTCUSDT"}, middleGap, middleGap.Add(15*time.Minute))
	if err != nil || len(coverage["BTCUSDT"]) != 1 {
		t.Fatalf("middle gap coverage=%#v error=%v", coverage, err)
	}
}

func integrationKlineRange(symbol string, start, end time.Time) []market.Kline {
	result := make([]market.Kline, 0, int(end.Sub(start)/(15*time.Minute)))
	for openTime := start; openTime.Before(end); openTime = openTime.Add(15 * time.Minute) {
		item := integrationKline(openTime)
		item.Symbol = symbol
		result = append(result, item)
	}
	return result
}
