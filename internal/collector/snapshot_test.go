package collector

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/marketdata"
	"github.com/shopspring/decimal"
)

type staticLatest map[string]market.MiniTicker

func (s staticLatest) Snapshot() map[string]market.MiniTicker { return s }

type recordingSnapshots struct {
	batches []market.SnapshotBatch
	result  market.SnapshotWriteResult
	gaps    int
}

func (r *recordingSnapshots) Save(_ context.Context, batch market.SnapshotBatch) (market.SnapshotWriteResult, error) {
	r.batches = append(r.batches, batch)
	return r.result, nil
}

func (r *recordingSnapshots) LoadRecent(context.Context, time.Time) ([]market.PricePoint, error) {
	return nil, nil
}

func (r *recordingSnapshots) MarkGaps(context.Context, time.Time) (int, error) {
	return r.gaps, nil
}

func TestSnapshotCollectorSamplesMinuteAndPersistsClosedFiveMinuteBucket(t *testing.T) {
	boundary := time.Date(2026, 8, 2, 12, 5, 0, 0, time.UTC)
	latest := staticLatest{
		"BTCUSDT":   ticker("BTCUSDT", boundary.Add(-10*time.Second), "101", "100"),
		"STALEUSDT": ticker("STALEUSDT", boundary.Add(-2*time.Minute), "1", "1"),
	}
	windows := marketdata.NewWindowStore(2 * time.Hour)
	repository := &recordingSnapshots{result: market.SnapshotWriteResult{Expected: 1, Actual: 1, Status: "SUCCEEDED"}}
	collector := newTestCollector(t, latest, windows, repository)

	if err := collector.Collect(context.Background(), boundary); err != nil {
		t.Fatal(err)
	}
	series := windows.Series("BTCUSDT")
	if len(series) != 1 || series[0].QualityScore != 100 {
		t.Fatalf("series = %#v", series)
	}
	if len(windows.Series("STALEUSDT")) != 0 {
		t.Fatal("stale ticker should not enter minute window")
	}
	if len(repository.batches) != 1 {
		t.Fatalf("batches = %d", len(repository.batches))
	}
	batch := repository.batches[0]
	if !batch.BucketStart.Equal(boundary.Add(-5*time.Minute)) || !batch.BucketEnd.Equal(boundary) {
		t.Fatalf("bucket = %s - %s", batch.BucketStart, batch.BucketEnd)
	}
	if len(batch.Items) != 1 || batch.Items[0].Ticker.Symbol != "BTCUSDT" {
		t.Fatalf("items = %#v", batch.Items)
	}
}

func TestSnapshotCollectorDoesNotPersistOrdinaryMinute(t *testing.T) {
	boundary := time.Date(2026, 8, 2, 12, 3, 0, 0, time.UTC)
	repository := &recordingSnapshots{}
	windows := marketdata.NewWindowStore(2 * time.Hour)
	collector := newTestCollector(t, staticLatest{
		"BTCUSDT": ticker("BTCUSDT", boundary, "101", "100"),
	}, windows, repository)
	if err := collector.Collect(context.Background(), boundary); err != nil {
		t.Fatal(err)
	}
	if len(repository.batches) != 0 || len(windows.Series("BTCUSDT")) != 1 {
		t.Fatalf("batches=%d series=%d", len(repository.batches), len(windows.Series("BTCUSDT")))
	}
}

func TestSnapshotCollectorHealthDegradesOnMissingSymbols(t *testing.T) {
	boundary := time.Date(2026, 8, 2, 12, 5, 0, 0, time.UTC)
	repository := &recordingSnapshots{result: market.SnapshotWriteResult{Expected: 2, Actual: 1, Missing: 1, Status: "DEGRADED"}}
	collector := newTestCollector(t, staticLatest{
		"BTCUSDT": ticker("BTCUSDT", boundary, "101", "100"),
	}, marketdata.NewWindowStore(2*time.Hour), repository)
	if err := collector.Collect(context.Background(), boundary); err != nil {
		t.Fatal(err)
	}
	healthy, persistedAt, message := collector.Health()
	if healthy || !persistedAt.Equal(boundary) || message == "" {
		t.Fatalf("health = %v, %s, %q", healthy, persistedAt, message)
	}
}

func newTestCollector(t *testing.T, latest LatestReader, windows WindowWriter, repository SnapshotRepository) *SnapshotCollector {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := NewSnapshotCollector(latest, windows, repository, 2*time.Hour, 90*time.Second, 5*time.Minute, logger)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func ticker(symbol string, eventTime time.Time, last, open string) market.MiniTicker {
	return market.MiniTicker{
		Symbol:         symbol,
		EventTime:      eventTime,
		ReceivedAt:     eventTime,
		LastPrice:      decimal.RequireFromString(last),
		OpenPrice24h:   decimal.RequireFromString(open),
		HighPrice24h:   decimal.RequireFromString(last),
		LowPrice24h:    decimal.RequireFromString(open),
		BaseVolume24h:  decimal.NewFromInt(10),
		QuoteVolume24h: decimal.NewFromInt(1000),
	}
}
