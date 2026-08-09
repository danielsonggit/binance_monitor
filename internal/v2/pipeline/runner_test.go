package pipeline

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"binance-monitor/internal/feature"
	"binance-monitor/internal/ranking"
)

type recordingCalculateAt struct {
	result Result
	err    error
	asOf   time.Time
}

func (r *recordingCalculateAt) RunAt(_ context.Context, asOf time.Time) (Result, error) {
	r.asOf = asOf
	return r.result, r.err
}

func TestRunnerCalculateUpdatesHealth(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	service := &recordingCalculateAt{result: Result{
		Features: feature.Result{AsOf: asOf, Symbols: 1, ValidMetrics: 4, Written: 1},
		Rankings: ranking.Result{AsOf: asOf, Groups: 8, Items: 8},
	}}
	runner, err := NewRunner(service, 5*time.Minute, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Calculate(context.Background(), asOf); err != nil {
		t.Fatal(err)
	}
	healthy, lastRun, message := runner.Health()
	if !healthy || !lastRun.Equal(asOf) || message != "" || !service.asOf.Equal(asOf) {
		t.Fatalf("health=%v last=%s message=%q service=%s", healthy, lastRun, message, service.asOf)
	}
}

func TestRunnerPreservesLastSuccessWhenNextCycleFails(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	service := &recordingCalculateAt{result: Result{Features: feature.Result{AsOf: asOf}}}
	runner, _ := NewRunner(service, 5*time.Minute, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Calculate(context.Background(), asOf); err != nil {
		t.Fatal(err)
	}
	service.err = errors.New("database unavailable")
	if err := runner.Calculate(context.Background(), asOf.Add(5*time.Minute)); err == nil {
		t.Fatal("expected error")
	}
	healthy, lastRun, message := runner.Health()
	if healthy || !lastRun.Equal(asOf) || message == "" {
		t.Fatalf("health=%v last=%s message=%q", healthy, lastRun, message)
	}
}

func TestRunnerRejectsUnalignedCycle(t *testing.T) {
	runner, _ := NewRunner(&recordingCalculateAt{}, 5*time.Minute, 5*time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := runner.Calculate(context.Background(), time.Date(2026, 8, 9, 12, 0, 1, 0, time.UTC)); err == nil {
		t.Fatal("expected alignment error")
	}
}
