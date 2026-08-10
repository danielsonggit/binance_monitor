package marketquery

import (
	"context"
	"errors"
	"testing"

	"binance-monitor/internal/domain/market"
)

type repositoryStub struct {
	sector  market.Sector
	horizon market.ReturnHorizon
	limit   int
	symbol  string
}

func (r *repositoryStub) LatestRanking(_ context.Context, sector market.Sector, horizon market.ReturnHorizon, limit int) (Ranking, error) {
	r.sector, r.horizon, r.limit = sector, horizon, limit
	return Ranking{Sector: sector, Horizon: horizon}, nil
}

func (r *repositoryStub) LatestFeature(_ context.Context, symbol string) (Feature, error) {
	r.symbol = symbol
	return Feature{Symbol: symbol}, nil
}

func (*repositoryStub) LatestQuality(context.Context) (Quality, error) { return Quality{}, nil }

func TestServiceValidatesRankingArguments(t *testing.T) {
	service, _ := NewService(&repositoryStub{})
	for _, test := range []struct {
		sector  market.Sector
		horizon market.ReturnHorizon
		limit   int
	}{
		{sector: "OTHER", horizon: market.ReturnHorizon1h, limit: 5},
		{sector: market.SectorCrypto, horizon: "2h", limit: 5},
		{sector: market.SectorCrypto, horizon: market.ReturnHorizon1h, limit: 0},
		{sector: market.SectorCrypto, horizon: market.ReturnHorizon1h, limit: 101},
	} {
		if _, err := service.Ranking(context.Background(), test.sector, test.horizon, test.limit); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Ranking(%q, %q, %d) error=%v", test.sector, test.horizon, test.limit, err)
		}
	}
}

func TestServiceNormalizesFeatureSymbol(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	feature, err := service.Feature(context.Background(), " btcusdt ")
	if err != nil || feature.Symbol != "BTCUSDT" || repository.symbol != "BTCUSDT" {
		t.Fatalf("feature=%#v symbol=%q err=%v", feature, repository.symbol, err)
	}
	if _, err := service.Feature(context.Background(), "  "); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("error=%v", err)
	}
}
