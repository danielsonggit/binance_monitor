package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/marketquery"
	"github.com/shopspring/decimal"
)

type fakeChecker struct {
	err error
}

type fakeMarketReader struct {
	ranking marketquery.Ranking
	feature marketquery.Feature
	quality marketquery.Quality
	err     error
	sector  market.Sector
	horizon market.ReturnHorizon
	limit   int
}

func (f *fakeMarketReader) Ranking(_ context.Context, sector market.Sector, horizon market.ReturnHorizon, limit int) (marketquery.Ranking, error) {
	f.sector, f.horizon, f.limit = sector, horizon, limit
	return f.ranking, f.err
}

func (f *fakeMarketReader) Feature(context.Context, string) (marketquery.Feature, error) {
	return f.feature, f.err
}

func (f *fakeMarketReader) Quality(context.Context) (marketquery.Quality, error) {
	return f.quality, f.err
}

func (f fakeChecker) Ping(context.Context) error { return f.err }

func TestLiveness(t *testing.T) {
	server := newTestServer(fakeChecker{})
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestRankingEndpointParsesFilters(t *testing.T) {
	reader := &fakeMarketReader{ranking: marketquery.Ranking{
		AsOf:  time.Date(2026, 8, 9, 13, 45, 0, 0, time.UTC),
		Items: []marketquery.RankingItem{{Symbol: "BTCUSDT", ReturnPercent: decimal.RequireFromString("1.25")}},
	}}
	server := newTestServerWithReader(fakeChecker{}, reader)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/rankings?sector=crypto&horizon=1h&limit=3", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || reader.sector != market.SectorCrypto || reader.horizon != market.ReturnHorizon1h || reader.limit != 3 {
		t.Fatalf("status=%d filter=%s/%s/%d body=%s", response.Code, reader.sector, reader.horizon, reader.limit, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "BTCUSDT") || !strings.Contains(response.Body.String(), "1.25") {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func TestRankingEndpointRejectsInvalidLimit(t *testing.T) {
	server := newTestServerWithReader(fakeChecker{}, &fakeMarketReader{})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/rankings?sector=CRYPTO&horizon=1h&limit=x", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_limit") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestFeatureEndpointMapsNotFound(t *testing.T) {
	server := newTestServerWithReader(fakeChecker{}, &fakeMarketReader{err: marketquery.ErrNotFound})
	request := httptest.NewRequest(http.MethodGet, "/api/v2/features/UNKNOWNUSDT", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestQualityEndpoint(t *testing.T) {
	reader := &fakeMarketReader{quality: marketquery.Quality{ActiveSymbols: 716, ValidMetrics: 2860}}
	server := newTestServerWithReader(fakeChecker{}, reader)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/quality", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "2860") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReadinessChecksDatabase(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "ready", want: http.StatusOK},
		{name: "database unavailable", err: errors.New("down"), want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(fakeChecker{err: test.err})
			request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func newTestServer(checker ReadinessChecker) *Server {
	return newTestServerWithReader(checker, &fakeMarketReader{})
}

func newTestServerWithReader(checker ReadinessChecker, reader MarketReader) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New("127.0.0.1:0", time.Second, checker, reader, logger)
}
