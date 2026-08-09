package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"binance-monitor/internal/universe"
)

type heartbeatCall struct {
	component string
	status    string
}

type recordingHeartbeats struct {
	mu    sync.Mutex
	calls []heartbeatCall
}

type fakeUniverse struct {
	mu     sync.Mutex
	calls  int
	result universe.Result
	err    error
}

type blockingMarket struct {
	connected bool
}

type blockingSnapshots struct {
	healthy bool
}

func (blockingSnapshots) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (b blockingSnapshots) Health() (bool, time.Time, string) {
	return b.healthy, time.Now(), ""
}

func (blockingMarket) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (b blockingMarket) Health() (bool, time.Time, string) {
	return b.connected, time.Now(), ""
}

func (f *fakeUniverse) Sync(context.Context) (universe.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.result, f.err
}

func (r *recordingHeartbeats) Record(_ context.Context, component, status string, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, heartbeatCall{component: component, status: status})
	return nil
}

func TestServiceRecordsLifecycle(t *testing.T) {
	recorder := &recordingHeartbeats{}
	universeSyncer := &fakeUniverse{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(
		recorder, universeSyncer,
		blockingMarket{connected: true}, blockingSnapshots{healthy: true}, blockingSnapshots{healthy: true},
		time.Millisecond, time.Hour, logger,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if len(recorder.calls) < 3 {
		t.Fatalf("heartbeat calls = %#v", recorder.calls)
	}
	if recorder.calls[0].status != "STARTING" || recorder.calls[1].status != "HEALTHY" {
		t.Fatalf("initial calls = %#v", recorder.calls[:2])
	}
	if recorder.calls[len(recorder.calls)-1].status != "STOPPING" {
		t.Fatalf("final call = %#v", recorder.calls[len(recorder.calls)-1])
	}
}

func TestServiceStaysDegradedWhenInitialUniverseSyncFails(t *testing.T) {
	recorder := &recordingHeartbeats{}
	universeSyncer := &fakeUniverse{err: errors.New("binance unavailable")}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(
		recorder, universeSyncer,
		blockingMarket{connected: true}, blockingSnapshots{healthy: true}, blockingSnapshots{healthy: true},
		time.Millisecond, time.Hour, logger,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Millisecond)
	defer cancel()
	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	foundDegraded := false
	for _, call := range recorder.calls {
		if call.status == "DEGRADED" {
			foundDegraded = true
		}
	}
	if !foundDegraded {
		t.Fatalf("heartbeat calls = %#v", recorder.calls)
	}
}

func TestServiceDegradesWhenSnapshotCollectorIsUnhealthy(t *testing.T) {
	recorder := &recordingHeartbeats{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(
		recorder,
		&fakeUniverse{},
		blockingMarket{connected: true},
		blockingSnapshots{healthy: false},
		blockingSnapshots{healthy: true},
		time.Millisecond,
		time.Hour,
		logger,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Millisecond)
	defer cancel()
	if err := service.Run(ctx); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, call := range recorder.calls {
		if call.status == "DEGRADED" {
			return
		}
	}
	t.Fatalf("heartbeat calls = %#v", recorder.calls)
}

func TestServiceDegradesWhenAnalysisRunnerIsUnhealthy(t *testing.T) {
	recorder := &recordingHeartbeats{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(
		recorder,
		&fakeUniverse{},
		blockingMarket{connected: true},
		blockingSnapshots{healthy: true},
		blockingSnapshots{healthy: false},
		time.Millisecond,
		time.Hour,
		logger,
	)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Millisecond)
	defer cancel()
	if err := service.Run(ctx); err != nil {
		t.Fatal(err)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	for _, call := range recorder.calls {
		if call.status == "DEGRADED" {
			return
		}
	}
	t.Fatalf("heartbeat calls = %#v", recorder.calls)
}
