package market

import "testing"

func TestAvailabilityClassifierUsesAuthoritativeStatusAndFreshness(t *testing.T) {
	classifier := NewBinanceUSDMAvailabilityClassifier()
	tests := []struct {
		name            string
		status          ExchangeStatus
		hasTicker       bool
		fresh           bool
		sourceAvailable bool
		want            AvailabilityState
	}{
		{name: "fresh trading", status: ExchangeStatusTrading, hasTicker: true, fresh: true, sourceAvailable: true, want: AvailabilityOpen},
		{name: "stale trading", status: ExchangeStatusTrading, hasTicker: true, sourceAvailable: true, want: AvailabilityLowActivity},
		{name: "missing trading", status: ExchangeStatusTrading, sourceAvailable: true, want: AvailabilityDataMissing},
		{name: "pending", status: ExchangeStatusPendingTrading, sourceAvailable: true, want: AvailabilityMarketClosed},
		{name: "settling", status: ExchangeStatusSettling, sourceAvailable: true, want: AvailabilityMarketClosed},
		{name: "unknown", status: ExchangeStatus("NEW_STATUS"), sourceAvailable: true, want: AvailabilityUnknown},
		{name: "source failure overrides closed", status: ExchangeStatusSettling, want: AvailabilitySourceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := classifier.Classify(AvailabilityInput{
				Instrument: Instrument{Symbol: "TESTUSDT", Sector: SectorTradFi, ExchangeStatus: test.status},
				HasTicker:  test.hasTicker, TickerFresh: test.fresh, SourceAvailable: test.sourceAvailable,
			})
			if got.State != test.want || got.Reason == "" {
				t.Fatalf("Classify() = %#v, want state %s", got, test.want)
			}
		})
	}
}

func TestSnapshotCoverageKeepsRawAndAdjustedCounts(t *testing.T) {
	coverage := NewSnapshotCoverage(BinanceUSDMAvailabilityRuleV1, []AvailabilityObservation{
		{State: AvailabilityOpen, TickerFresh: true},
		{State: AvailabilityMarketClosed, TickerFresh: true},
		{State: AvailabilityLowActivity},
		{State: AvailabilityDataMissing},
		{State: AvailabilitySourceUnavailable},
		{State: AvailabilityUnknown},
	})
	if err := coverage.Validate(); err != nil {
		t.Fatal(err)
	}
	if coverage.RawExpected != 6 || coverage.RawActual != 2 || coverage.RawMissing != 4 {
		t.Fatalf("raw coverage = %#v", coverage)
	}
	if coverage.RawCoveragePercent.String() != "33.333333" || coverage.AdjustedCoveragePercent.String() != "20" {
		t.Fatalf("coverage percentages = %#v", coverage)
	}
	if coverage.AdjustedExpected != 5 || coverage.AdjustedActual != 1 || coverage.AdjustedMissing != 4 {
		t.Fatalf("adjusted coverage = %#v", coverage)
	}
	if coverage.StateCounts[AvailabilityLowActivity] != 1 || coverage.StateCounts[AvailabilityMarketClosed] != 1 {
		t.Fatalf("state counts = %#v", coverage.StateCounts)
	}
}

func TestSnapshotCoverageHealthUsesAdjustedDenominator(t *testing.T) {
	coverage := SnapshotCoverage{
		RuleVersion: BinanceUSDMAvailabilityRuleV1,
		RawExpected: 10, RawActual: 8, RawMissing: 2,
		AdjustedExpected: 8, AdjustedActual: 8, AdjustedMissing: 0,
	}
	if !coverage.Healthy(90) {
		t.Fatal("adjusted coverage should be healthy")
	}
}
