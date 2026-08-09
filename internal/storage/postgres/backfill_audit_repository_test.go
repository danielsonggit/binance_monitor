package postgres

import (
	"errors"
	"testing"
	"time"

	"binance-monitor/internal/backfill"
)

func TestBackfillGapAuditStatusInputs(t *testing.T) {
	start := time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC)
	target := backfill.Gap{Symbol: "BTCUSDT", Start: start, End: start.Add(time.Hour), Count: 4}
	remaining := []backfill.Gap{
		{Symbol: "BTCUSDT", Start: start.Add(15 * time.Minute), End: start.Add(45 * time.Minute), Count: 2},
		{Symbol: "ETHUSDT", Start: start, End: start.Add(time.Hour), Count: 4},
	}
	if count := overlappingGapCount(target, remaining); count != 2 {
		t.Fatalf("overlap count = %d", count)
	}
	failures := []backfill.Failure{
		{Symbol: "ETHUSDT", Start: start, End: start.Add(time.Hour), Err: errors.New("wrong")},
		{Symbol: "BTCUSDT", Start: start, End: start.Add(time.Hour), Err: errors.New("last error")},
	}
	if message := lastGapError(target, failures); message != "last error" {
		t.Fatalf("last error = %q", message)
	}
}
