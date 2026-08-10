package reporter

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"binance-monitor/internal/marketquery"
	"binance-monitor/internal/notification"
	v2report "binance-monitor/internal/v2/report"
)

type reporterClock struct{ now time.Time }

func (c reporterClock) Now() time.Time { return c.now }

type reportBuilderStub struct {
	snapshot v2report.Snapshot
	calls    int
}

func (r *reportBuilderStub) Messages(context.Context) (v2report.Snapshot, []string, error) {
	r.calls++
	return r.snapshot, []string{"message"}, nil
}

type enqueuerStub struct {
	request notification.EnqueueRequest
	calls   int
}

func (e *enqueuerStub) Enqueue(_ context.Context, request notification.EnqueueRequest) (notification.EnqueueResult, error) {
	e.calls++
	e.request = request
	return notification.EnqueueResult{OutboxID: 1, Created: e.calls == 1}, nil
}

type dispatcherStub struct {
	calls    int
	recovers int
}

func (d *dispatcherStub) Recover(context.Context, time.Time) error {
	d.recovers++
	return nil
}
func (d *dispatcherStub) DispatchOne(context.Context, time.Time) (bool, error) {
	d.calls++
	return false, nil
}

func TestRunCycleEnqueuesDueSlotWithDatabaseIdempotencyKey(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 8, 10, 4, 3, 0, 0, location)
	slot := time.Date(2026, 8, 10, 4, 0, 0, 0, location)
	reports := &reportBuilderStub{snapshot: v2report.Snapshot{
		AsOf: slot.UTC(), Quality: marketquery.Quality{AsOf: slot.UTC()},
	}}
	enqueuer := &enqueuerStub{}
	dispatcher := &dispatcherStub{}
	service, err := New(
		reports, enqueuer, dispatcher, reporterClock{now: now}, location,
		[]int{0, 4, 8}, 10*time.Minute, time.Second, 3,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunCycle(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Enqueued || !result.Created || reports.calls != 1 || enqueuer.calls != 1 || dispatcher.recovers != 1 {
		t.Fatalf("result=%#v reports=%d enqueue=%d", result, reports.calls, enqueuer.calls)
	}
	if enqueuer.request.IdempotencyKey != "scheduled-market-report:2026-08-09T20:00:00Z" ||
		!enqueuer.request.ScheduledFor.Equal(slot.UTC()) {
		t.Fatalf("request=%#v", enqueuer.request)
	}
}

func TestDueSlotObeysGrace(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	if _, due := DueSlot(time.Date(2026, 8, 10, 4, 9, 59, 0, location), []int{4}, 10*time.Minute); !due {
		t.Fatal("expected due")
	}
	if _, due := DueSlot(time.Date(2026, 8, 10, 4, 10, 0, 0, location), []int{4}, 10*time.Minute); due {
		t.Fatal("expected outside grace")
	}
}
