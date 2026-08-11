package backfill

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"binance-monitor/internal/binancevision"
	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

type memoryRepository struct {
	mu        sync.Mutex
	symbols   []string
	existing  map[string]map[int64]struct{}
	available map[string]time.Time
}

func (m *memoryRepository) AvailableFrom(_ context.Context, symbols []string) (map[string]time.Time, error) {
	result := make(map[string]time.Time, len(symbols))
	for _, symbol := range symbols {
		if value := m.available[symbol]; !value.IsZero() {
			result[symbol] = value
		}
	}
	return result, nil
}

func (m *memoryRepository) ActiveSymbols(context.Context) ([]string, error) {
	return append([]string(nil), m.symbols...), nil
}

func (m *memoryRepository) ExistingOpenTimes(_ context.Context, symbols []string, start, end time.Time) (map[string][]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string][]time.Time)
	for _, symbol := range symbols {
		for value := range m.existing[symbol] {
			openTime := time.UnixMilli(value).UTC()
			if !openTime.Before(start) && openTime.Before(end) {
				result[symbol] = append(result[symbol], openTime)
			}
		}
	}
	return result, nil
}

func (m *memoryRepository) Save(_ context.Context, batch market.KlineBatch) (market.KlineWriteResult, error) {
	if err := batch.Validate(); err != nil {
		return market.KlineWriteResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, item := range batch.Items {
		if m.existing[item.Symbol] == nil {
			m.existing[item.Symbol] = make(map[int64]struct{})
		}
		m.existing[item.Symbol][item.OpenTime.UnixMilli()] = struct{}{}
	}
	return market.KlineWriteResult{Attempted: len(batch.Items), Upserted: len(batch.Items)}, nil
}

type fakeArchive struct{ err error }

type fixedClock struct{ now time.Time }

func (f fixedClock) Now() time.Time { return f.now }

func (f fakeArchive) FetchDailyKlines(_ context.Context, symbol string, _ market.KlineInterval, day time.Time) ([]market.Kline, error) {
	if f.err != nil {
		return nil, f.err
	}
	return klineRange(symbol, day, day.Add(24*time.Hour)), nil
}

type fakeREST struct {
	mu    sync.Mutex
	calls int
}

func (f *fakeREST) FetchKlines(_ context.Context, query market.KlineQuery) ([]market.Kline, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return klineRange(query.Symbol, query.StartTime, query.EndTime.Add(time.Millisecond)), nil
}

func (f *fakeREST) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestServiceUsesArchiveThenRESTAndVerifiesFinalCoverage(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 7, 0, 0, time.UTC)
	repository := &memoryRepository{symbols: []string{"BTCUSDT"}, existing: make(map[string]map[int64]struct{})}
	rest := &fakeREST{}
	service, err := NewService(repository, fakeArchive{}, rest, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), 30*time.Hour, 8)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expected != 120 || result.PresentBefore != 0 || result.ArchiveDays != 1 ||
		result.RESTRequests != 1 || result.Remaining != 0 || result.Written != 144 || len(result.Failures) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceFallsBackToRESTOnlyWhenArchiveMissing(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	repository := &memoryRepository{symbols: []string{"BTCUSDT"}, existing: make(map[string]map[int64]struct{})}
	rest := &fakeREST{}
	service, _ := NewService(repository, fakeArchive{err: binancevision.ErrArchiveNotFound}, rest, fixedClock{now: now})
	result, err := service.Run(context.Background(), 30*time.Hour, 8)
	if err != nil {
		t.Fatal(err)
	}
	if result.ArchiveDays != 0 || result.RESTRequests != 2 || result.Remaining != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceDoesNotBackfillBeforeContractOnboardTime(t *testing.T) {
	now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	repository := &memoryRepository{
		symbols:   []string{"NEWUSDT"},
		existing:  make(map[string]map[int64]struct{}),
		available: map[string]time.Time{"NEWUSDT": now.Add(-55 * time.Minute)},
	}
	rest := &fakeREST{}
	service, err := NewService(repository, fakeArchive{}, rest, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), 30*time.Hour, 8)
	if err != nil {
		t.Fatal(err)
	}
	if result.Expected != 4 || result.Written != 4 || result.Remaining != 0 || result.RESTRequests != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceDoesNotHideCorruptArchiveWithRESTFallback(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	repository := &memoryRepository{symbols: []string{"BTCUSDT"}, existing: make(map[string]map[int64]struct{})}
	rest := &fakeREST{}
	corrupt := errors.New("checksum failed")
	service, _ := NewService(repository, fakeArchive{err: corrupt}, rest, fixedClock{now: now})
	result, err := service.Run(context.Background(), 30*time.Hour, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) != 1 || !errors.Is(result.Failures[0].Err, corrupt) || result.RESTRequests != 0 || result.Remaining != 120 || rest.callCount() != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceRejectsInvalidConcurrency(t *testing.T) {
	repository := &memoryRepository{symbols: []string{"BTCUSDT"}, existing: make(map[string]map[int64]struct{})}
	service, _ := NewService(repository, fakeArchive{}, &fakeREST{}, fixedClock{now: time.Now()})
	if _, err := service.Run(context.Background(), 30*time.Hour, 0); err == nil {
		t.Fatal("expected concurrency error")
	}
}

func TestServiceRejectsEmptyUniverse(t *testing.T) {
	repository := &memoryRepository{existing: make(map[string]map[int64]struct{})}
	service, _ := NewService(repository, fakeArchive{}, &fakeREST{}, fixedClock{now: time.Now()})
	if _, err := service.Run(context.Background(), 30*time.Hour, 1); err == nil {
		t.Fatal("expected empty universe error")
	}
}

func klineRange(symbol string, start, end time.Time) []market.Kline {
	result := make([]market.Kline, 0)
	for openTime := start; openTime.Before(end); openTime = openTime.Add(15 * time.Minute) {
		result = append(result, market.Kline{
			Symbol: symbol, Interval: market.KlineInterval15m,
			OpenTime: openTime, CloseTime: openTime.Add(15*time.Minute - time.Millisecond),
			Open: decimal.NewFromInt(100), High: decimal.NewFromInt(110), Low: decimal.NewFromInt(95), Close: decimal.NewFromInt(105),
			Volume: decimal.NewFromInt(10), QuoteVolume: decimal.NewFromInt(1000), TradeCount: 10,
			TakerBuyBaseVolume: decimal.NewFromInt(5), TakerBuyQuoteVolume: decimal.NewFromInt(500),
		})
	}
	return result
}
