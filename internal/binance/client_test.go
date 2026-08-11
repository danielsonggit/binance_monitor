package binance

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"binance-monitor/internal/httpjson"
	"binance-monitor/internal/model"
)

func TestFetchMarketClassifiesAndFiltersContracts(t *testing.T) {
	clientHTTP := &http.Client{
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			var payload string
			switch request.URL.Path {
			case "/fapi/v1/exchangeInfo":
				payload = `{
					"symbols": [
						{"symbol":"KORUUSDT","baseAsset":"KORU","quoteAsset":"USDT","status":"TRADING","contractType":"TRADIFI_PERPETUAL","underlyingType":"EQUITY","underlyingSubType":["TECH"],"pricePrecision":2,"quantityPrecision":1},
						{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","status":"TRADING","contractType":"PERPETUAL","underlyingType":"COIN","underlyingSubType":[],"pricePrecision":1,"quantityPrecision":3},
						{"symbol":"ETHUSDC","baseAsset":"ETH","quoteAsset":"USDC","status":"TRADING","contractType":"PERPETUAL","underlyingType":"COIN","underlyingSubType":[]},
						{"symbol":"OLDUSDT","baseAsset":"OLD","quoteAsset":"USDT","status":"SETTLING","contractType":"PERPETUAL","underlyingType":"COIN","underlyingSubType":[]},
						{"symbol":"BTCUSD_260925","baseAsset":"BTC","quoteAsset":"USD","status":"TRADING","contractType":"CURRENT_QUARTER","underlyingType":"COIN","underlyingSubType":[]}
					]
				}`
			case "/fapi/v1/ticker/24hr":
				payload = `[
					{"symbol":"KORUUSDT","lastPrice":"19.35","priceChangePercent":"4.821","quoteVolume":"1000","closeTime":1785168020000},
					{"symbol":"BTCUSDT","lastPrice":"100000","priceChangePercent":"-1.25","quoteVolume":"2000","closeTime":1785168020000},
					{"symbol":"ETHUSDC","lastPrice":"3500","priceChangePercent":"2","quoteVolume":"3000","closeTime":1785168020000}
				]`
			default:
				t.Fatalf("unexpected path %s", request.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(payload)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	client := New(
		"https://example.test",
		httpjson.NewWithHTTPClient(clientHTTP, 1),
	)
	contracts, tickers, err := client.FetchMarket(context.Background(), []string{"USDT"})
	if err != nil {
		t.Fatalf("FetchMarket() error = %v", err)
	}
	if len(contracts) != 2 {
		t.Fatalf("len(contracts) = %d, want 2", len(contracts))
	}
	if contracts["KORUUSDT"].Board != model.BoardTradFi {
		t.Errorf("KORU board = %s", contracts["KORUUSDT"].Board)
	}
	if contracts["BTCUSDT"].Board != model.BoardCrypto {
		t.Errorf("BTC board = %s", contracts["BTCUSDT"].Board)
	}
	if contracts["BTCUSDT"].PricePrecision != 1 || contracts["BTCUSDT"].QuantityPrecision != 3 {
		t.Errorf("BTC precision = %d/%d", contracts["BTCUSDT"].PricePrecision, contracts["BTCUSDT"].QuantityPrecision)
	}
	if _, exists := contracts["ETHUSDC"]; exists {
		t.Error("USDC contract should be filtered")
	}
	if len(tickers) != 3 {
		t.Errorf("len(tickers) = %d, want 3", len(tickers))
	}
}

func TestFetchActiveInstrumentsReturnsStableDomainModels(t *testing.T) {
	clientHTTP := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		payload := `{"symbols":[
			{"symbol":"KORUUSDT","baseAsset":"KORU","quoteAsset":"USDT","status":"TRADING","contractType":"TRADIFI_PERPETUAL","pricePrecision":2,"quantityPrecision":1},
			{"symbol":"BTCUSDT","baseAsset":"BTC","quoteAsset":"USDT","status":"TRADING","contractType":"PERPETUAL","pricePrecision":1,"quantityPrecision":3,"onboardDate":1786413600000}
		]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})}
	client := New("https://example.test", httpjson.NewWithHTTPClient(clientHTTP, 1))
	instruments, err := client.FetchActiveInstruments(context.Background(), []string{"USDT"})
	if err != nil {
		t.Fatal(err)
	}
	if len(instruments) != 2 || instruments[0].Symbol != "BTCUSDT" || instruments[1].Sector != "TRADFI" {
		t.Fatalf("instruments = %#v", instruments)
	}
	if instruments[0].OnboardTime.UnixMilli() != 1786413600000 {
		t.Fatalf("onboard time = %s", instruments[0].OnboardTime)
	}
}

func TestFetchMarketUsesOneSharedLimiterForEveryRESTRequest(t *testing.T) {
	clientHTTP := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		payload := `{"symbols":[]}`
		if request.URL.Path == "/fapi/v1/ticker/24hr" {
			payload = `[]`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
		}, nil
	})}
	limiter := &recordingWeightLimiter{}
	client, err := NewWithWeightLimiter(
		"https://example.test",
		httpjson.NewWithHTTPClient(clientHTTP, 1),
		limiter,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.FetchMarket(context.Background(), []string{"USDT"}); err != nil {
		t.Fatal(err)
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.weights) != 2 || limiter.weights[0] != 1 || limiter.weights[1] != ticker24hAllSymbolsWeight {
		t.Fatalf("weights = %#v", limiter.weights)
	}
}

func TestParseTickersIgnoresInvalidRows(t *testing.T) {
	rows := []tickerResponse{
		{Symbol: "GOODUSDT", LastPrice: "1.5", PriceChangePercent: "3.2"},
		{Symbol: "BADUSDT", LastPrice: "oops", PriceChangePercent: "3.2"},
		{Symbol: "ZEROUSDT", LastPrice: "0", PriceChangePercent: "1"},
		{Symbol: "CHANGEUSDT", LastPrice: "2", PriceChangePercent: "oops"},
	}
	result := ParseTickers(rows)
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if result["GOODUSDT"].LastPrice != 1.5 {
		t.Errorf("price = %v", result["GOODUSDT"].LastPrice)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
