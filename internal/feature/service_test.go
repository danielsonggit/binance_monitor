package feature

import (
	"context"
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/shopspring/decimal"
)

type memoryFeatureRepository struct {
	inputs  []market.ReturnFeatureInput
	batches []market.ReturnFeatureBatch
}

func (r *memoryFeatureRepository) LoadReturnInputs(context.Context, time.Time, time.Duration) ([]market.ReturnFeatureInput, error) {
	return r.inputs, nil
}

func (r *memoryFeatureRepository) SaveReturnFeatures(
	_ context.Context,
	batch market.ReturnFeatureBatch,
) (market.ReturnFeatureWriteResult, error) {
	if err := batch.Validate(); err != nil {
		return market.ReturnFeatureWriteResult{}, err
	}
	r.batches = append(r.batches, batch)
	return market.ReturnFeatureWriteResult{Attempted: len(batch.Items), Upserted: len(batch.Items)}, nil
}

type fixedFeatureClock struct{ now time.Time }

func (c fixedFeatureClock) Now() time.Time { return c.now }

func TestServiceCalculatesAndPersistsAlignedBatch(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 7, 30, 0, time.UTC)
	asOf := now.Truncate(5 * time.Minute)
	input := completeInput("BTCUSDT", asOf.Truncate(15*time.Minute))
	input.Prices = append(input.Prices, market.FeaturePricePoint{
		ObservedAt: asOf, Price: decimal.NewFromInt(200), Source: market.PriceSourceSnapshot5m, QualityScore: 100,
	})
	repository := &memoryFeatureRepository{inputs: []market.ReturnFeatureInput{input}}
	service, err := NewService(repository, testCalculator(t), fixedFeatureClock{now: now}, 25*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.AsOf.Equal(time.Date(2026, 8, 9, 12, 5, 0, 0, time.UTC)) || result.Symbols != 1 ||
		result.ValidMetrics != 4 || result.InvalidMetrics != 0 || result.Written != 1 || len(repository.batches) != 1 {
		t.Fatalf("result=%#v batches=%d", result, len(repository.batches))
	}
}

func TestServiceRecordsInvalidReasonCounts(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	input := completeInput("BTCUSDT", asOf)
	input.Prices = nil
	repository := &memoryFeatureRepository{inputs: []market.ReturnFeatureInput{input}}
	service, _ := NewService(repository, testCalculator(t), fixedFeatureClock{now: asOf}, 25*time.Hour)
	result, err := service.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.InvalidMetrics != 4 || result.InvalidReasons[InvalidCurrentMissing] != 4 {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceRejectsEmptyUniverse(t *testing.T) {
	asOf := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	service, _ := NewService(&memoryFeatureRepository{}, testCalculator(t), fixedFeatureClock{now: asOf}, 25*time.Hour)
	if _, err := service.Run(context.Background()); err == nil {
		t.Fatal("expected empty universe error")
	}
}
