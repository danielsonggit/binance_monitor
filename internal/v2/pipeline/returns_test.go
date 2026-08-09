package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"binance-monitor/internal/backfill"
	"binance-monitor/internal/feature"
)

type fakeHistory struct {
	result backfill.Result
	err    error
	calls  int
}

func (f *fakeHistory) Run(context.Context, time.Duration, int) (backfill.Result, error) {
	f.calls++
	return f.result, f.err
}

type fakeFeatures struct {
	result feature.Result
	calls  int
}

type fakeAuditor struct {
	calls int
	err   error
}

func (f *fakeAuditor) Record(context.Context, backfill.Result, time.Time, time.Time) error {
	f.calls++
	return f.err
}

func (f *fakeFeatures) RunAt(context.Context, time.Time) (feature.Result, error) {
	f.calls++
	return f.result, nil
}

func TestReturnPipelineBackfillsBeforeCalculating(t *testing.T) {
	history := &fakeHistory{}
	auditor := &fakeAuditor{}
	features := &fakeFeatures{result: feature.Result{Symbols: 2}}
	pipeline, err := NewReturnPipeline(history, auditor, features, 30*time.Hour, 8)
	if err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.RunAt(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if history.calls != 1 || auditor.calls != 1 || features.calls != 1 || result.Symbols != 2 {
		t.Fatalf("history=%d audit=%d features=%d result=%#v", history.calls, auditor.calls, features.calls, result)
	}
}

func TestReturnPipelineStopsOnBackfillSystemError(t *testing.T) {
	history := &fakeHistory{err: errors.New("database unavailable")}
	features := &fakeFeatures{}
	pipeline, _ := NewReturnPipeline(history, &fakeAuditor{}, features, 30*time.Hour, 8)
	if _, err := pipeline.RunAt(context.Background(), time.Now()); err == nil || features.calls != 0 {
		t.Fatalf("error=%v feature calls=%d", err, features.calls)
	}
}

func TestReturnPipelineCalculatesAndLetsQualityGateHandleSymbolGaps(t *testing.T) {
	history := &fakeHistory{result: backfill.Result{
		Remaining: 1, Failures: []backfill.Failure{{Err: errors.New("one symbol unavailable")}},
	}}
	features := &fakeFeatures{result: feature.Result{InvalidMetrics: 1}}
	pipeline, _ := NewReturnPipeline(history, &fakeAuditor{}, features, 30*time.Hour, 8)
	result, err := pipeline.RunAt(context.Background(), time.Now())
	if err != nil || features.calls != 1 || result.InvalidMetrics != 1 {
		t.Fatalf("error=%v feature calls=%d result=%#v", err, features.calls, result)
	}
}
