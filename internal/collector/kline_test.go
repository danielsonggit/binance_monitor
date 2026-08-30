package collector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

type fakeKlineSource struct {
	items []market.Kline
	err   error
	query market.KlineQuery
	calls int
}

func (f *fakeKlineSource) FetchKlines(
	ctx context.Context,
	query market.KlineQuery,
) ([]market.Kline, error) {
	f.calls++
	f.query = query
	if f.err != nil {
		return nil, f.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]market.Kline(nil), f.items...), nil
}

type fakeKlineRepository struct {
	batch  market.KlineBatch
	result market.KlineWriteResult
	err    error
	calls  int
}

func (f *fakeKlineRepository) Save(
	ctx context.Context,
	batch market.KlineBatch,
) (market.KlineWriteResult, error) {
	f.calls++
	f.batch = batch
	if f.err != nil {
		return market.KlineWriteResult{}, f.err
	}
	if err := ctx.Err(); err != nil {
		return market.KlineWriteResult{}, err
	}
	return f.result, nil
}

type fixedClock struct {
	now time.Time
}

func (f fixedClock) Now() time.Time { return f.now }

func TestKlineCollectorNormalizesFiltersAndPersistsCompletedCandles(t *testing.T) {
	completed := collectorTestKline(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	incomplete := collectorTestKline(completed.OpenTime.Add(15 * time.Minute))
	now := completed.CloseTime.Add(time.Millisecond)
	source := &fakeKlineSource{items: []market.Kline{completed, incomplete}}
	repository := &fakeKlineRepository{result: market.KlineWriteResult{Attempted: 1, Upserted: 1}}
	collector, err := NewKlineCollector(source, repository, fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}

	write, result, err := collector.Collect(context.Background(), market.KlineQuery{
		Symbol:   " btcusdt ",
		Interval: market.KlineInterval15m,
		Limit:    500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.query.Symbol != "BTCUSDT" {
		t.Fatalf("source query = %#v", source.query)
	}
	if repository.calls != 1 || len(repository.batch.Items) != 1 || repository.batch.Items[0] != completed {
		t.Fatalf("repository batch = %#v, calls = %d", repository.batch, repository.calls)
	}
	if repository.batch.Source != market.KlineSourceBinanceFutures || !repository.batch.ReceivedAt.Equal(now) {
		t.Fatalf("repository metadata = %#v", repository.batch)
	}
	if write.Attempted != 1 || write.Upserted != 1 {
		t.Fatalf("write result = %#v", write)
	}
	if result.Fetched != 2 || result.Completed != 1 || result.Dropped != 1 || result.Upserted != 1 {
		t.Fatalf("collection result = %#v", result)
	}
}

func TestKlineCollectorSkipsRepositoryWhenNoCompletedCandles(t *testing.T) {
	kline := collectorTestKline(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	source := &fakeKlineSource{items: []market.Kline{kline}}
	repository := &fakeKlineRepository{}
	collector, err := NewKlineCollector(source, repository, fixedClock{now: kline.CloseTime.Add(-time.Millisecond)})
	if err != nil {
		t.Fatal(err)
	}

	_, result, err := collector.Collect(context.Background(), market.KlineQuery{
		Symbol:   "BTCUSDT",
		Interval: market.KlineInterval15m,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repository.calls != 0 || result.Fetched != 1 || result.Completed != 0 || result.Dropped != 1 {
		t.Fatalf("repository calls = %d, result = %#v", repository.calls, result)
	}
}

func TestKlineCollectorRejectsMismatchedAndDuplicateSourceData(t *testing.T) {
	base := collectorTestKline(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	tests := []struct {
		name    string
		items   []market.Kline
		wantErr string
	}{
		{
			name: "mismatched symbol",
			items: []market.Kline{func() market.Kline {
				candidate := base
				candidate.Symbol = "ETHUSDT"
				return candidate
			}()},
			wantErr: "不匹配",
		},
		{name: "duplicate", items: []market.Kline{base, base}, wantErr: "重复"},
		{
			name: "invalid candle",
			items: []market.Kline{func() market.Kline {
				candidate := base
				candidate.Close = decimal.Zero
				return candidate
			}()},
			wantErr: "校验来源",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &fakeKlineSource{items: test.items}
			repository := &fakeKlineRepository{}
			collector, err := NewKlineCollector(source, repository, fixedClock{now: base.CloseTime.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = collector.Collect(context.Background(), market.KlineQuery{
				Symbol:   "BTCUSDT",
				Interval: market.KlineInterval15m,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Collect() error = %v, want containing %q", err, test.wantErr)
			}
			if repository.calls != 0 {
				t.Fatalf("repository calls = %d", repository.calls)
			}
		})
	}
}

func TestKlineCollectorPropagatesSourceRepositoryAndCancellationErrors(t *testing.T) {
	base := collectorTestKline(time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC))
	sentinel := errors.New("upstream failed")
	query := market.KlineQuery{Symbol: "BTCUSDT", Interval: market.KlineInterval15m}

	sourceFailure, _ := NewKlineCollector(
		&fakeKlineSource{err: sentinel},
		&fakeKlineRepository{},
		fixedClock{now: base.CloseTime.Add(time.Hour)},
	)
	if _, _, err := sourceFailure.Collect(context.Background(), query); !errors.Is(err, sentinel) {
		t.Fatalf("source error = %v", err)
	}

	repositoryFailure, _ := NewKlineCollector(
		&fakeKlineSource{items: []market.Kline{base}},
		&fakeKlineRepository{err: sentinel},
		fixedClock{now: base.CloseTime.Add(time.Hour)},
	)
	if _, _, err := repositoryFailure.Collect(context.Background(), query); !errors.Is(err, sentinel) {
		t.Fatalf("repository error = %v", err)
	}

	cancelled, _ := NewKlineCollector(
		&fakeKlineSource{items: []market.Kline{base}},
		&fakeKlineRepository{},
		fixedClock{now: base.CloseTime.Add(time.Hour)},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cancelled.Collect(ctx, query); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
}

func collectorTestKline(openTime time.Time) market.Kline {
	return market.Kline{
		Symbol:              "BTCUSDT",
		Interval:            market.KlineInterval15m,
		OpenTime:            openTime,
		CloseTime:           openTime.Add(15*time.Minute - time.Millisecond),
		Open:                decimal.RequireFromString("100"),
		High:                decimal.RequireFromString("110"),
		Low:                 decimal.RequireFromString("95"),
		Close:               decimal.RequireFromString("105"),
		Volume:              decimal.RequireFromString("12.5"),
		QuoteVolume:         decimal.RequireFromString("1300"),
		TradeCount:          42,
		TakerBuyBaseVolume:  decimal.RequireFromString("7"),
		TakerBuyQuoteVolume: decimal.RequireFromString("730"),
	}
}
