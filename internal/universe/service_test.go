package universe

import (
	"context"
	"errors"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

type fakeSource struct {
	instruments []market.Instrument
	err         error
	quotes      []string
}

func (f *fakeSource) FetchActiveInstruments(_ context.Context, quotes []string) ([]market.Instrument, error) {
	f.quotes = append([]string(nil), quotes...)
	return f.instruments, f.err
}

type fakeRepository struct {
	active   int
	snapshot Snapshot
	result   Result
	err      error
}

func (f *fakeRepository) ActiveCount(context.Context) (int, error) { return f.active, f.err }
func (f *fakeRepository) Reconcile(_ context.Context, snapshot Snapshot) (Result, error) {
	f.snapshot = snapshot
	return f.result, f.err
}

type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

func TestSyncReconcilesValidSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 34, 56, 0, time.FixedZone("CST", 8*60*60))
	source := &fakeSource{instruments: []market.Instrument{instrument("BTCUSDT")}}
	repository := &fakeRepository{active: 1, result: Result{Updated: 1}}
	service := New(source, repository, fixedClock{now}, []string{"USDT"}, 80, 2)

	result, err := service.Sync(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated != 1 || repository.snapshot.MissingConfirmations != 2 {
		t.Fatalf("result=%#v snapshot=%#v", result, repository.snapshot)
	}
	wantTime := time.Date(2026, 8, 2, 4, 34, 0, 0, time.UTC)
	if !repository.snapshot.ObservedAt.Equal(wantTime) {
		t.Fatalf("ObservedAt = %s, want %s", repository.snapshot.ObservedAt, wantTime)
	}
}

func TestSyncRejectsSuspiciousContraction(t *testing.T) {
	source := &fakeSource{instruments: makeInstruments(79)}
	repository := &fakeRepository{active: 100}
	service := New(source, repository, fixedClock{time.Now()}, []string{"USDT"}, 80, 2)
	if _, err := service.Sync(context.Background()); err == nil {
		t.Fatal("expected contraction error")
	}
	if !repository.snapshot.ObservedAt.IsZero() {
		t.Fatal("repository should not be called")
	}
}

func TestSyncAcceptsConfiguredBoundary(t *testing.T) {
	source := &fakeSource{instruments: makeInstruments(80)}
	repository := &fakeRepository{active: 100}
	service := New(source, repository, fixedClock{time.Now()}, []string{"USDT"}, 80, 2)
	if _, err := service.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestSyncRejectsEmptyDuplicateAndInvalidSnapshots(t *testing.T) {
	tests := []struct {
		name        string
		instruments []market.Instrument
	}{
		{name: "empty"},
		{name: "duplicate", instruments: []market.Instrument{instrument("BTCUSDT"), instrument("BTCUSDT")}},
		{name: "invalid", instruments: []market.Instrument{{Symbol: "BTCUSDT"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := New(
				&fakeSource{instruments: test.instruments},
				&fakeRepository{},
				fixedClock{time.Now()},
				[]string{"USDT"},
				80,
				2,
			)
			if _, err := service.Sync(context.Background()); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSyncWrapsSourceError(t *testing.T) {
	service := New(
		&fakeSource{err: errors.New("network")},
		&fakeRepository{},
		fixedClock{time.Now()},
		[]string{"USDT"},
		80,
		2,
	)
	if _, err := service.Sync(context.Background()); err == nil {
		t.Fatal("expected source error")
	}
}

func instrument(symbol string) market.Instrument {
	return market.Instrument{
		Symbol:       symbol,
		BaseAsset:    symbol,
		QuoteAsset:   "USDT",
		Sector:       market.SectorCrypto,
		ContractType: "PERPETUAL",
	}
}

func makeInstruments(count int) []market.Instrument {
	result := make([]market.Instrument, count)
	for index := range result {
		result[index] = instrument(time.Unix(int64(index), 0).UTC().Format("150405") + "USDT")
	}
	return result
}
