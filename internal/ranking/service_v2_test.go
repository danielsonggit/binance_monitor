package ranking

import (
	"context"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
)

type rankingRepositoryStub struct {
	inputs []market.RankingInput
	batch  market.RankingBatch
}

func (r *rankingRepositoryStub) LoadRankingInputs(context.Context, time.Time, string) ([]market.RankingInput, error) {
	return r.inputs, nil
}

func (r *rankingRepositoryStub) SaveRankings(_ context.Context, batch market.RankingBatch) (market.RankingWriteResult, error) {
	r.batch = batch
	items := 0
	for _, group := range batch.Groups {
		items += len(group.Items)
	}
	return market.RankingWriteResult{GroupsUpserted: len(batch.Groups), ItemsWritten: items}, nil
}

type rankingClock struct{ now time.Time }

func (c rankingClock) Now() time.Time { return c.now }

func TestServiceLoadsCalculatesAndSaves(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	repository := &rankingRepositoryStub{inputs: []market.RankingInput{
		rankingInput("BTCUSDT", market.SectorCrypto, "1", "100", true),
		rankingInput("XAUUSDT", market.SectorTradFi, "0.5", "50", true),
	}}
	service, err := NewService(repository, testCalculator(t, 5), rankingClock{now: asOf.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.RunAt(context.Background(), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if result.Groups != 8 || result.Items != 8 || result.ActiveMetrics != 8 || result.Eligible != 8 || result.Positive != 8 || result.Written != 8 {
		t.Fatalf("result=%#v", result)
	}
	if len(repository.batch.Groups) != 8 || !repository.batch.AsOf.Equal(asOf) {
		t.Fatalf("batch=%#v", repository.batch)
	}
}
