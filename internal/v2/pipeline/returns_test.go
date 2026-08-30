package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"binance-monitor/internal/backfill"
	"binance-monitor/internal/domain/signal"
	"binance-monitor/internal/feature"
	"binance-monitor/internal/ranking"
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

type fakeRankings struct {
	result ranking.Result
	err    error
	calls  int
}

type fakeCandidates struct {
	result signal.CandidateWriteResult
	err    error
	calls  int
}

func (f *fakeCandidates) RunAt(context.Context, time.Time) (signal.CandidateWriteResult, error) {
	f.calls++
	return f.result, f.err
}

func (f *fakeRankings) RunAt(context.Context, time.Time) (ranking.Result, error) {
	f.calls++
	return f.result, f.err
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
	rankings := &fakeRankings{result: ranking.Result{Groups: 8}}
	candidates := &fakeCandidates{result: signal.CandidateWriteResult{Active: 3}}
	pipeline, err := NewReturnPipeline(history, auditor, features, rankings, candidates, 30*time.Hour, 8)
	if err != nil {
		t.Fatal(err)
	}
	result, err := pipeline.RunAt(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if history.calls != 1 || auditor.calls != 1 || features.calls != 1 || rankings.calls != 1 || candidates.calls != 1 ||
		result.Features.Symbols != 2 || result.Rankings.Groups != 8 || result.Candidates.Active != 3 {
		t.Fatalf("history=%d audit=%d features=%d rankings=%d result=%#v", history.calls, auditor.calls, features.calls, rankings.calls, result)
	}
}

func TestReturnPipelineStopsOnBackfillSystemError(t *testing.T) {
	history := &fakeHistory{err: errors.New("database unavailable")}
	features := &fakeFeatures{}
	rankings := &fakeRankings{}
	pipeline, _ := NewReturnPipeline(history, &fakeAuditor{}, features, rankings, &fakeCandidates{}, 30*time.Hour, 8)
	if _, err := pipeline.RunAt(context.Background(), time.Now()); err == nil || features.calls != 0 {
		t.Fatalf("error=%v feature calls=%d", err, features.calls)
	}
}

func TestReturnPipelineCalculatesAndLetsQualityGateHandleSymbolGaps(t *testing.T) {
	history := &fakeHistory{result: backfill.Result{
		Remaining: 1, Failures: []backfill.Failure{{Err: errors.New("one symbol unavailable")}},
	}}
	features := &fakeFeatures{result: feature.Result{InvalidMetrics: 1}}
	rankings := &fakeRankings{}
	pipeline, _ := NewReturnPipeline(history, &fakeAuditor{}, features, rankings, &fakeCandidates{}, 30*time.Hour, 8)
	result, err := pipeline.RunAt(context.Background(), time.Now())
	if err != nil || features.calls != 1 || rankings.calls != 1 || result.Features.InvalidMetrics != 1 {
		t.Fatalf("error=%v feature calls=%d result=%#v", err, features.calls, result)
	}
}

func TestReturnPipelineDoesNotRankWhenFeatureCalculationFails(t *testing.T) {
	features := &fakeFeaturesWithError{err: errors.New("feature write failed")}
	rankings := &fakeRankings{}
	pipeline, _ := NewReturnPipeline(&fakeHistory{}, &fakeAuditor{}, features, rankings, &fakeCandidates{}, 30*time.Hour, 8)
	if _, err := pipeline.RunAt(context.Background(), time.Now()); err == nil || rankings.calls != 0 {
		t.Fatalf("error=%v ranking calls=%d", err, rankings.calls)
	}
}

func TestReturnPipelineSurfacesRankingFailureAfterFeatures(t *testing.T) {
	rankings := &fakeRankings{err: errors.New("ranking write failed")}
	pipeline, _ := NewReturnPipeline(&fakeHistory{}, &fakeAuditor{}, &fakeFeatures{result: feature.Result{Written: 2}}, rankings, &fakeCandidates{}, 30*time.Hour, 8)
	result, err := pipeline.RunAt(context.Background(), time.Now())
	if err == nil || result.Features.Written != 2 {
		t.Fatalf("error=%v result=%#v", err, result)
	}
}

func TestReturnPipelineSurfacesCandidateFailureAfterRankings(t *testing.T) {
	candidates := &fakeCandidates{err: errors.New("candidate write failed")}
	pipeline, _ := NewReturnPipeline(
		&fakeHistory{}, &fakeAuditor{}, &fakeFeatures{result: feature.Result{Written: 2}},
		&fakeRankings{result: ranking.Result{Groups: 8}}, candidates, 30*time.Hour, 8,
	)
	result, err := pipeline.RunAt(context.Background(), time.Now())
	if err == nil || result.Rankings.Groups != 8 || candidates.calls != 1 {
		t.Fatalf("error=%v result=%#v calls=%d", err, result, candidates.calls)
	}
}

type fakeFeaturesWithError struct{ err error }

func (f *fakeFeaturesWithError) RunAt(context.Context, time.Time) (feature.Result, error) {
	return feature.Result{}, f.err
}
