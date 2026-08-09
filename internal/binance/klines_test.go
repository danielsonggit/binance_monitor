package binance

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/httpjson"
)

type recordingWeightLimiter struct {
	mu      sync.Mutex
	weights []int
	err     error
}

func (r *recordingWeightLimiter) Wait(_ context.Context, weight int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.weights = append(r.weights, weight)
	return r.err
}

func TestFetchKlinesBuildsQueryAndParsesDomainModel(t *testing.T) {
	const openTimeMS int64 = 1499040000000
	const closeTimeMS int64 = 1499040899999
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/fapi/v1/klines" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		query := request.URL.Query()
		assertQueryValue(t, query.Get("symbol"), "BTCUSDT", "symbol")
		assertQueryValue(t, query.Get("interval"), "15m", "interval")
		assertQueryValue(t, query.Get("startTime"), strconv.FormatInt(openTimeMS, 10), "startTime")
		assertQueryValue(t, query.Get("endTime"), strconv.FormatInt(openTimeMS+3600000, 10), "endTime")
		assertQueryValue(t, query.Get("limit"), "100", "limit")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[[
			1499040000000,
			"100.100000000000000001",
			"110.2",
			"95.3",
			"105.4",
			"12.5",
			1499040899999,
			"1300.6",
			42,
			"7.1",
			"730.2",
			"0"
		]]`))
	}))
	defer server.Close()

	client := New(server.URL, httpjson.NewWithHTTPClient(server.Client(), 1))
	klines, err := client.FetchKlines(context.Background(), KlineRequest{
		Symbol:    " btcusdt ",
		Interval:  market.KlineInterval15m,
		StartTime: time.UnixMilli(openTimeMS),
		EndTime:   time.UnixMilli(openTimeMS + 3600000),
		Limit:     100,
	})
	if err != nil {
		t.Fatalf("FetchKlines() error = %v", err)
	}
	if len(klines) != 1 {
		t.Fatalf("len(klines) = %d, want 1", len(klines))
	}
	kline := klines[0]
	if kline.Symbol != "BTCUSDT" || kline.Interval != market.KlineInterval15m {
		t.Errorf("identity = %s/%s", kline.Symbol, kline.Interval)
	}
	if kline.OpenTime.UnixMilli() != openTimeMS || kline.CloseTime.UnixMilli() != closeTimeMS {
		t.Errorf("times = %d/%d", kline.OpenTime.UnixMilli(), kline.CloseTime.UnixMilli())
	}
	if kline.Open.String() != "100.100000000000000001" || kline.Close.String() != "105.4" {
		t.Errorf("prices = %s/%s", kline.Open, kline.Close)
	}
	if kline.Volume.String() != "12.5" || kline.QuoteVolume.String() != "1300.6" {
		t.Errorf("volumes = %s/%s", kline.Volume, kline.QuoteVolume)
	}
	if kline.TradeCount != 42 || kline.TakerBuyBaseVolume.String() != "7.1" ||
		kline.TakerBuyQuoteVolume.String() != "730.2" {
		t.Errorf(
			"trade detail = %d/%s/%s",
			kline.TradeCount,
			kline.TakerBuyBaseVolume,
			kline.TakerBuyQuoteVolume,
		)
	}
}

func TestFetchKlinesOmitsOptionalQueryParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		for _, key := range []string{"startTime", "endTime", "limit"} {
			if _, exists := query[key]; exists {
				t.Errorf("query unexpectedly contains %s", key)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := New(server.URL, httpjson.NewWithHTTPClient(server.Client(), 1))
	klines, err := client.FetchKlines(context.Background(), KlineRequest{
		Symbol:   "BTCUSDT",
		Interval: market.KlineInterval15m,
	})
	if err != nil {
		t.Fatalf("FetchKlines() error = %v", err)
	}
	if len(klines) != 0 {
		t.Fatalf("len(klines) = %d, want 0", len(klines))
	}
}

func TestFetchKlinesRejectsInvalidRequestBeforeNetwork(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := New(server.URL, httpjson.NewWithHTTPClient(server.Client(), 1))

	epoch := time.Unix(0, 0)
	tests := []struct {
		name    string
		request KlineRequest
		wantErr string
	}{
		{
			name:    "missing symbol",
			request: KlineRequest{Interval: market.KlineInterval15m},
			wantErr: "symbol",
		},
		{
			name:    "unsupported interval",
			request: KlineRequest{Symbol: "BTCUSDT", Interval: "1h"},
			wantErr: "不支持",
		},
		{
			name: "start is not before end",
			request: KlineRequest{
				Symbol:    "BTCUSDT",
				Interval:  market.KlineInterval15m,
				StartTime: epoch.Add(time.Hour),
				EndTime:   epoch.Add(time.Hour),
			},
			wantErr: "必须早于",
		},
		{
			name: "start before epoch",
			request: KlineRequest{
				Symbol:    "BTCUSDT",
				Interval:  market.KlineInterval15m,
				StartTime: epoch.Add(-time.Millisecond),
			},
			wantErr: "Unix epoch",
		},
		{
			name:    "negative limit",
			request: KlineRequest{Symbol: "BTCUSDT", Interval: market.KlineInterval15m, Limit: -1},
			wantErr: "limit",
		},
		{
			name:    "limit too high",
			request: KlineRequest{Symbol: "BTCUSDT", Interval: market.KlineInterval15m, Limit: 1501},
			wantErr: "limit",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := client.FetchKlines(context.Background(), test.request)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("FetchKlines() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("network calls = %d, want 0", calls.Load())
	}
}

func TestFetchKlinesRejectsMalformedRows(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		wantErr string
	}{
		{
			name:    "too few fields",
			payload: `[[1499040000000,"100"]]`,
			wantErr: "字段数量",
		},
		{
			name: "invalid decimal",
			payload: `[[1499040000000,"bad","110","95","105","12",` +
				`1499040899999,"1300",42,"7","730","0"]]`,
			wantErr: "open 不是有效十进制数",
		},
		{
			name: "invalid close boundary",
			payload: `[[1499040000000,"100","110","95","105","12",` +
				`1499040900000,"1300",42,"7","730","0"]]`,
			wantErr: "close time",
		},
		{
			name: "invalid ohlc",
			payload: `[[1499040000000,"100","90","95","105","12",` +
				`1499040899999,"1300",42,"7","730","0"]]`,
			wantErr: "OHLC 关系",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.payload))
			}))
			defer server.Close()

			client := New(server.URL, httpjson.NewWithHTTPClient(server.Client(), 1))
			_, err := client.FetchKlines(context.Background(), KlineRequest{
				Symbol:   "BTCUSDT",
				Interval: market.KlineInterval15m,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("FetchKlines() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestFetchKlinesPreservesHTTPStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"code":-1121,"msg":"Invalid symbol."}`))
	}))
	defer server.Close()

	client := New(server.URL, httpjson.NewWithHTTPClient(server.Client(), 1))
	_, err := client.FetchKlines(context.Background(), KlineRequest{
		Symbol:   "UNKNOWNUSDT",
		Interval: market.KlineInterval15m,
	})
	if err == nil {
		t.Fatal("FetchKlines() error = nil")
	}
	var statusErr *httpjson.StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("FetchKlines() error type = %T, want *httpjson.StatusError: %v", err, err)
	}
	if statusErr.Code != http.StatusBadRequest || !strings.Contains(statusErr.Body, "Invalid symbol") {
		t.Fatalf("status error = %#v", statusErr)
	}
}

func TestKlineRequestWeightUsesBinanceLimitTiers(t *testing.T) {
	tests := []struct {
		limit int
		want  int
	}{
		{limit: 0, want: 5},
		{limit: 1, want: 1},
		{limit: 99, want: 1},
		{limit: 100, want: 2},
		{limit: 499, want: 2},
		{limit: 500, want: 5},
		{limit: 1000, want: 5},
		{limit: 1001, want: 10},
		{limit: 1500, want: 10},
	}
	for _, test := range tests {
		weight, err := KlineRequestWeight(test.limit)
		if err != nil || weight != test.want {
			t.Errorf("KlineRequestWeight(%d) = %d, %v; want %d", test.limit, weight, err, test.want)
		}
	}
	if _, err := KlineRequestWeight(1501); err == nil {
		t.Fatal("KlineRequestWeight(1501) error = nil")
	}
}

func TestFetchKlinesWaitsForSharedRequestWeightBeforeNetwork(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[]`))
	}))
	defer server.Close()

	limiter := &recordingWeightLimiter{}
	client, err := NewWithWeightLimiter(server.URL, httpjson.NewWithHTTPClient(server.Client(), 1), limiter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.FetchKlines(context.Background(), KlineRequest{
		Symbol:   "BTCUSDT",
		Interval: market.KlineInterval15m,
		Limit:    1500,
	}); err != nil {
		t.Fatal(err)
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if calls.Load() != 1 || len(limiter.weights) != 1 || limiter.weights[0] != 10 {
		t.Fatalf("calls=%d weights=%v", calls.Load(), limiter.weights)
	}
}

func TestFetchKlinesDoesNotCallNetworkWhenLimiterFails(t *testing.T) {
	var calls atomic.Int64
	httpClient := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected network call")
	})}
	limiter := &recordingWeightLimiter{err: context.Canceled}
	client, err := NewWithWeightLimiter("https://example.test", httpjson.NewWithHTTPClient(httpClient, 1), limiter)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.FetchKlines(context.Background(), KlineRequest{
		Symbol:   "BTCUSDT",
		Interval: market.KlineInterval15m,
		Limit:    100,
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchKlines() error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("network calls = %d", calls.Load())
	}
}

func assertQueryValue(t *testing.T, got, want, name string) {
	t.Helper()
	if got != want {
		t.Errorf("query %s = %q, want %q", name, got, want)
	}
}
