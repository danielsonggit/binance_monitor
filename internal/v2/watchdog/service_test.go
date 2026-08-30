package watchdog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type sequenceProbe struct {
	results []ProbeResult
	index   int
}

func (p *sequenceProbe) Check(context.Context) ProbeResult {
	result := p.results[p.index]
	if p.index+1 < len(p.results) {
		p.index++
	}
	return result
}

type memoryStateStore struct{ state State }

func (s *memoryStateStore) Load(context.Context) (State, error) { return s.state, nil }
func (s *memoryStateStore) Save(_ context.Context, state State) error {
	s.state = state
	return nil
}

type notificationCall struct{ chatID, message string }
type recordingNotifier struct {
	calls []notificationCall
	err   error
}

func (n *recordingNotifier) Send(_ context.Context, chatID, message string) error {
	n.calls = append(n.calls, notificationCall{chatID: chatID, message: message})
	return n.err
}

type ambiguousTestError struct{ error }

func (ambiguousTestError) Ambiguous() bool { return true }

func TestServiceDebouncesAlertsPersistsDedupAndRecovers(t *testing.T) {
	start := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	probe := &sequenceProbe{results: []ProbeResult{
		{CheckedAt: start, Reasons: []string{"API down"}},
		{CheckedAt: start.Add(time.Minute), Reasons: []string{"API down"}},
		{CheckedAt: start.Add(2 * time.Minute), Reasons: []string{"API down"}},
		{CheckedAt: start.Add(3 * time.Minute), Reasons: []string{"API down"}},
		{CheckedAt: start.Add(4 * time.Minute), Healthy: true},
		{CheckedAt: start.Add(5 * time.Minute), Healthy: true},
	}}
	store := &memoryStateStore{}
	notifier := &recordingNotifier{}
	service := newTestService(t, probe, store, notifier)
	for range 2 {
		_, state, err := service.RunOnce(context.Background())
		if err != nil || state.Active || len(notifier.calls) != 0 {
			t.Fatalf("before threshold state=%#v calls=%d err=%v", state, len(notifier.calls), err)
		}
	}
	_, state, err := service.RunOnce(context.Background())
	if err != nil || !state.Active || len(notifier.calls) != 2 {
		t.Fatalf("alert state=%#v calls=%d err=%v", state, len(notifier.calls), err)
	}
	// A later failure and a reconstructed service must not resend the incident.
	service = newTestService(t, probe, store, notifier)
	_, state, err = service.RunOnce(context.Background())
	if err != nil || len(notifier.calls) != 2 || !state.Active {
		t.Fatalf("dedup state=%#v calls=%d err=%v", state, len(notifier.calls), err)
	}
	_, state, _ = service.RunOnce(context.Background())
	if !state.Active || state.RecoveryCount != 1 || len(notifier.calls) != 2 {
		t.Fatalf("first recovery state=%#v calls=%d", state, len(notifier.calls))
	}
	_, state, _ = service.RunOnce(context.Background())
	if state.Active || len(notifier.calls) != 4 || notifier.calls[2].message == notifier.calls[0].message {
		t.Fatalf("recovered state=%#v calls=%#v", state, notifier.calls)
	}
}

func TestServiceTreatsAmbiguousNotificationAsDelivered(t *testing.T) {
	start := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	probe := &sequenceProbe{results: []ProbeResult{{CheckedAt: start, Reasons: []string{"down"}}}}
	store := &memoryStateStore{}
	notifier := &recordingNotifier{err: ambiguousTestError{errors.New("timeout")}}
	service, err := NewService(
		probe, store, notifier, []string{"-1"}, 1, 2, time.Minute, time.UTC,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, state, err := service.RunOnce(context.Background())
	if err != nil || !state.AlertSent["-1"] {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	_, _, _ = service.RunOnce(context.Background())
	if len(notifier.calls) != 1 {
		t.Fatalf("ambiguous notification was retried: %d", len(notifier.calls))
	}
}

func newTestService(t *testing.T, probe Probe, store StateStore, notifier Notifier) *Service {
	t.Helper()
	service, err := NewService(
		probe, store, notifier, []string{"-1", "-2"}, 3, 2, time.Minute, time.UTC,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
