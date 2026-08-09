package backfill

import (
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

func TestBuildPlanFindsLeadingMiddleAndTrailingGaps(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 7, 0, 0, time.UTC)
	start := time.Date(2026, 8, 9, 11, 0, 0, 0, time.UTC)
	plan, err := BuildPlan(now, time.Hour, market.KlineInterval15m, []string{" ethusdt ", "BTCUSDT"}, map[string][]time.Time{
		"BTCUSDT": {start.Add(15 * time.Minute), start.Add(45 * time.Minute)},
		"ETHUSDT": {start, start.Add(15 * time.Minute), start.Add(30 * time.Minute), start.Add(45 * time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.WindowStart.Equal(start) || !plan.WindowEnd.Equal(time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = [%s,%s)", plan.WindowStart, plan.WindowEnd)
	}
	if plan.Expected != 8 || plan.Present != 6 || len(plan.Gaps) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Gaps[0].Symbol != "BTCUSDT" || plan.Gaps[0].Count != 1 || !plan.Gaps[0].Start.Equal(start) {
		t.Fatalf("first gap = %#v", plan.Gaps[0])
	}
	if !plan.Gaps[1].Start.Equal(start.Add(30*time.Minute)) || plan.Gaps[1].Count != 1 {
		t.Fatalf("second gap = %#v", plan.Gaps[1])
	}
}

func TestBuildPlanMergesContiguousMissingCandles(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	plan, err := BuildPlan(now, time.Hour, market.KlineInterval15m, []string{"BTCUSDT"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Gaps) != 1 || plan.Gaps[0].Count != 4 ||
		!plan.Gaps[0].Start.Equal(now.Add(-time.Hour)) || !plan.Gaps[0].End.Equal(now) {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestBuildPlanRejectsInvalidInputsAndUnalignedStoredTime(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if _, err := BuildPlan(now, 10*time.Minute, market.KlineInterval15m, []string{"BTCUSDT"}, nil); err == nil {
		t.Fatal("expected invalid lookback error")
	}
	if _, err := BuildPlan(now, time.Hour, market.KlineInterval15m, []string{"BTCUSDT", "btcusdt"}, nil); err == nil {
		t.Fatal("expected duplicate symbol error")
	}
	_, err := BuildPlan(now, time.Hour, market.KlineInterval15m, []string{"BTCUSDT"}, map[string][]time.Time{
		"BTCUSDT": {now.Add(-time.Hour).Add(time.Minute)},
	})
	if err == nil || !strings.Contains(err.Error(), "未对齐") {
		t.Fatalf("error = %v", err)
	}
}
