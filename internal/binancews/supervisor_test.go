package binancews

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

type fakeSession struct {
	events chan []market.MiniTicker
	errors chan error
	mu     sync.Mutex
	closed int
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		events: make(chan []market.MiniTicker, 4),
		errors: make(chan error, 1),
	}
}

func (f *fakeSession) Events() <-chan []market.MiniTicker { return f.events }
func (f *fakeSession) Errors() <-chan error               { return f.errors }
func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

type fakeConnector struct {
	mu       sync.Mutex
	sessions []*fakeSession
	calls    int
}

func (f *fakeConnector) Connect(context.Context) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	session := newFakeSession()
	f.sessions = append(f.sessions, session)
	return session, nil
}

type recordingSink struct {
	mu      sync.Mutex
	batches int
}

func (r *recordingSink) Apply([]market.MiniTicker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches++
}

func TestSupervisorConsumesEvents(t *testing.T) {
	connector := &fakeConnector{}
	sink := &recordingSink{}
	supervisor := testSupervisor(connector, sink, time.Second, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	session := waitForSession(t, connector, 1)
	session.events <- []market.MiniTicker{{Symbol: "BTCUSDT"}}
	waitForBatches(t, sink, 1)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if supervisor.Status().Connected {
		t.Fatal("supervisor should be disconnected after shutdown")
	}
}

func TestSupervisorReconnectsStaleSession(t *testing.T) {
	connector := &fakeConnector{}
	supervisor := testSupervisor(connector, &recordingSink{}, 5*time.Millisecond, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := supervisor.Run(ctx); err != nil {
		t.Fatal(err)
	}
	connector.mu.Lock()
	defer connector.mu.Unlock()
	if connector.calls < 2 {
		t.Fatalf("connect calls = %d", connector.calls)
	}
}

func TestSupervisorRotatesConnection(t *testing.T) {
	connector := &fakeConnector{}
	supervisor := testSupervisor(connector, &recordingSink{}, time.Hour, 5*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := supervisor.Run(ctx); err != nil {
		t.Fatal(err)
	}
	connector.mu.Lock()
	defer connector.mu.Unlock()
	if connector.calls < 2 {
		t.Fatalf("connect calls = %d", connector.calls)
	}
}

func TestSupervisorReconnectsWhenEventChannelCloses(t *testing.T) {
	connector := &fakeConnector{}
	supervisor := testSupervisor(connector, &recordingSink{}, time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	first := waitForSession(t, connector, 1)
	close(first.events)
	waitForSession(t, connector, 2)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func testSupervisor(connector Connector, sink Sink, stale, rotate time.Duration) *Supervisor {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewSupervisor(connector, sink, stale, rotate, time.Millisecond, logger)
}

func waitForSession(t *testing.T, connector *fakeConnector, count int) *fakeSession {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		connector.mu.Lock()
		if len(connector.sessions) >= count {
			session := connector.sessions[count-1]
			connector.mu.Unlock()
			return session
		}
		connector.mu.Unlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for session")
	return nil
}

func waitForBatches(t *testing.T, sink *recordingSink, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		batches := sink.batches
		sink.mu.Unlock()
		if batches >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for batches")
}
