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
	if coverage.RawCoveragePercent.String() != "33.333333" || coverage.AdjustedCoveragePercent.String() != "20" ||
		coverage.OperationalCoveragePercent.String() != "40" {
		t.Fatalf("coverage percentages = %#v", coverage)
	}
	if coverage.AdjustedExpected != 5 || coverage.AdjustedActual != 1 || coverage.AdjustedMissing != 4 {
		t.Fatalf("adjusted coverage = %#v", coverage)
	}
	if coverage.OperationalExpected != 5 || coverage.OperationalActual != 2 || coverage.OperationalMissing != 3 {
		t.Fatalf("operational coverage = %#v", coverage)
	}
	if coverage.StateCounts[AvailabilityLowActivity] != 1 || coverage.StateCounts[AvailabilityMarketClosed] != 1 {
		t.Fatalf("state counts = %#v", coverage.StateCounts)
	}
}

func TestSnapshotCoverageHealthUsesOperationalCoverage(t *testing.T) {
	coverage := NewSnapshotCoverage(BinanceUSDMAvailabilityRuleV1, []AvailabilityObservation{
		{State: AvailabilityOpen, TickerFresh: true},
		{State: AvailabilityOpen, TickerFresh: true},
		{State: AvailabilityLowActivity},
		{State: AvailabilityLowActivity},
		{State: AvailabilityMarketClosed},
	})
	if !coverage.Healthy(90) {
		t.Fatal("low activity should not make the source unhealthy")
	}
}

func TestSnapshotCoverageHealthRejectsOperationalFailures(t *testing.T) {
	coverage := NewSnapshotCoverage(BinanceUSDMAvailabilityRuleV1, []AvailabilityObservation{
		{State: AvailabilityOpen, TickerFresh: true},
		{State: AvailabilityDataMissing},
		{State: AvailabilitySourceUnavailable},
		{State: AvailabilityUnknown},
	})
	if coverage.Healthy(85) {
		t.Fatal("missing, unavailable and unknown observations must reduce operational health")
	}
}

func TestSnapshotCoverageHealthFallsBackForHistoricalRecords(t *testing.T) {
	coverage := SnapshotCoverage{
		RuleVersion:      BinanceUSDMAvailabilityRuleV1,
		AdjustedExpected: 10, AdjustedActual: 7, AdjustedMissing: 3,
		StateCounts: map[AvailabilityState]int{AvailabilityOpen: 7, AvailabilityLowActivity: 2},
	}
	coverage.EnsureOperationalCoverage()
	if coverage.OperationalExpected != 10 || coverage.OperationalActual != 9 ||
		coverage.OperationalMissing != 1 || !coverage.Healthy(90) {
		t.Fatalf("historical operational coverage = %#v", coverage)
	}
}
