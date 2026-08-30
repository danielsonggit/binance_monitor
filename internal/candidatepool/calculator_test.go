package candidatepool

import (
	"testing"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/domain/signal"
	"github.com/shopspring/decimal"
)

func TestCalculatorAppliesLiquidityCapacityAndExistingPriority(t *testing.T) {
	rules := testRules()
	calculator, err := NewCalculator(rules)
	if err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 8, 30, 3, 0, 0, 0, time.UTC)
	inputs := []signal.CandidateInput{
		candidateInput("AUSDT", "6", "6"),
		candidateInput("BUSDT", "5", "5"),
		candidateInput("CUSDT", "0", "0"),
		candidateInput("DUSDT", "10", "10"),
		candidateInput("EUSDT", "0", "0"),
	}
	inputs[3].QuoteVolume24h = decimal.NewFromInt(10)
	batch, err := calculator.Calculate(asOf, asOf.Add(time.Second), inputs, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, batch, "AUSDT", signal.CandidateEntered)
	assertOutcome(t, batch, "BUSDT", signal.CandidateRejectedCapacity)
	assertOutcome(t, batch, "CUSDT", signal.CandidateRejectedMomentum)
	assertOutcome(t, batch, "DUSDT", signal.CandidateRejectedLiquidity)
	if len(batch.Members) != 1 || batch.Members[0].Symbol != "AUSDT" {
		t.Fatalf("members=%#v", batch.Members)
	}

	existing := []signal.CandidateMember{{
		InstrumentID: 2, Symbol: "BUSDT", Sector: market.SectorCrypto, Direction: signal.DirectionLong,
		RuleVersion: rules.RuleVersion, Status: signal.CandidateMemberActive,
		EnteredAt: asOf.Add(-time.Hour), LastSelectedAt: asOf.Add(-5 * time.Minute),
	}}
	batch, err = calculator.Calculate(asOf, asOf.Add(time.Second), inputs, existing)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, batch, "BUSDT", signal.CandidateContinued)
	assertOutcome(t, batch, "AUSDT", signal.CandidateRejectedCapacity)
}

func TestCalculatorHoldsThreeMissesAndEnforcesCooldown(t *testing.T) {
	rules := testRules()
	calculator, _ := NewCalculator(rules)
	asOf := time.Date(2026, 8, 30, 4, 0, 0, 0, time.UTC)
	members := []signal.CandidateMember{{
		InstrumentID: 1, Symbol: "AUSDT", Sector: market.SectorCrypto, Direction: signal.DirectionLong,
		RuleVersion: rules.RuleVersion, Status: signal.CandidateMemberActive,
		EnteredAt: asOf.Add(-time.Hour), LastSelectedAt: asOf.Add(-5 * time.Minute),
	}}
	miss := []signal.CandidateInput{candidateInput("AUSDT", "0", "0")}
	for index := 0; index < 3; index++ {
		batch, err := calculator.Calculate(asOf.Add(time.Duration(index)*5*time.Minute), asOf.Add(time.Second), miss, members)
		if err != nil {
			t.Fatal(err)
		}
		members = batch.Members
		want := signal.CandidateMissHeld
		if index == 2 {
			want = signal.CandidateExited
		}
		assertOutcome(t, batch, "AUSDT", want)
	}
	if len(members) != 1 || members[0].Status != signal.CandidateMemberCooldown ||
		!members[0].CooldownUntil.Equal(asOf.Add(40*time.Minute)) {
		t.Fatalf("cooldown member=%#v", members)
	}

	hit := []signal.CandidateInput{candidateInput("AUSDT", "4", "4"), candidateInput("BUSDT", "0", "0")}
	batch, err := calculator.Calculate(asOf.Add(15*time.Minute), asOf.Add(time.Second), hit, members)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, batch, "AUSDT", signal.CandidateRejectedCooldown)
	batch, err = calculator.Calculate(asOf.Add(40*time.Minute), asOf.Add(41*time.Minute), hit, batch.Members)
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, batch, "AUSDT", signal.CandidateEntered)
}

func TestCandidateRuleChecksumIsStable(t *testing.T) {
	rules := signal.CandidateRulesV1()
	first, err := rules.Checksum()
	if err != nil {
		t.Fatal(err)
	}
	second, err := rules.Checksum()
	if err != nil || first != second || len(first) != 64 {
		t.Fatalf("checksums=%q/%q err=%v", first, second, err)
	}
}

func TestCalculatorDoesNotMergeRelistedInstrumentWithSameSymbol(t *testing.T) {
	rules := testRules()
	crypto := rules.Sectors[market.SectorCrypto]
	crypto.Capacity = 2
	rules.Sectors[market.SectorCrypto] = crypto
	calculator, _ := NewCalculator(rules)
	asOf := time.Date(2026, 8, 30, 5, 0, 0, 0, time.UTC)
	input := candidateInput("AUSDT", "4", "4")
	input.InstrumentID = 2
	oldMember := signal.CandidateMember{
		InstrumentID: 1, Symbol: "AUSDT", Sector: market.SectorCrypto,
		Direction: signal.DirectionLong, RuleVersion: rules.RuleVersion, Status: signal.CandidateMemberActive,
		EnteredAt: asOf.Add(-time.Hour), LastSelectedAt: asOf.Add(-5 * time.Minute),
	}
	batch, err := calculator.Calculate(asOf, asOf.Add(time.Second), []signal.CandidateInput{input}, []signal.CandidateMember{oldMember})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Evaluations) != 2 || len(batch.Members) != 2 {
		t.Fatalf("relisted batch=%#v", batch)
	}
	outcomes := make(map[int64]signal.CandidateOutcome)
	for _, evaluation := range batch.Evaluations {
		outcomes[evaluation.InstrumentID] = evaluation.Outcome
	}
	if outcomes[1] != signal.CandidateMissHeld || outcomes[2] != signal.CandidateEntered {
		t.Fatalf("relisted outcomes=%#v", outcomes)
	}
}

func TestCalculatorDoesNotScoreNonOpenOutlierAgainstOpenDistribution(t *testing.T) {
	rules := testRules()
	calculator, _ := NewCalculator(rules)
	asOf := time.Date(2026, 8, 30, 6, 0, 0, 0, time.UTC)
	inputs := []signal.CandidateInput{
		candidateInput("AUSDT", "0", "0"),
		candidateInput("BUSDT", "1", "1"),
		candidateInput("CUSDT", "100", "100"),
	}
	inputs[2].Availability = market.AvailabilityLowActivity

	batch, err := calculator.Calculate(asOf, asOf.Add(time.Second), inputs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, evaluation := range batch.Evaluations {
		if evaluation.Symbol != "CUSDT" {
			continue
		}
		if evaluation.Outcome != signal.CandidateRejectedQuality || evaluation.Trigger15m || evaluation.Trigger1h ||
			!evaluation.Percentile15m.IsZero() || !evaluation.Percentile1h.IsZero() {
			t.Fatalf("non-open outlier was scored: %#v", evaluation)
		}
		return
	}
	t.Fatal("CUSDT evaluation not found")
}

func testRules() signal.CandidateRuleSet {
	rules := signal.CandidateRulesV1()
	crypto := rules.Sectors[market.SectorCrypto]
	crypto.Return15mFloorPercent = decimal.NewFromInt(1)
	crypto.Return1hFloorPercent = decimal.NewFromInt(1)
	crypto.Return15mPercentile = decimal.RequireFromString("0.5")
	crypto.Return1hPercentile = decimal.RequireFromString("0.5")
	crypto.MinimumQuoteVolume1hUSD = decimal.NewFromInt(100)
	crypto.MinimumQuoteVolume24hUSD = decimal.NewFromInt(100)
	crypto.Capacity = 1
	rules.Sectors[market.SectorCrypto] = crypto
	return rules
}

func candidateInput(symbol, return15m, return1h string) signal.CandidateInput {
	return signal.CandidateInput{
		InstrumentID: int64(symbol[0]-'A') + 1,
		Symbol:       symbol, Sector: market.SectorCrypto, Availability: market.AvailabilityOpen,
		Return15m: decimal.RequireFromString(return15m), Return1h: decimal.RequireFromString(return1h),
		Valid15m: true, Valid1h: true, RecentQuoteVolume1h: decimal.NewFromInt(1_000),
		QuoteVolume24h: decimal.NewFromInt(10_000),
	}
}

func assertOutcome(t *testing.T, batch signal.CandidateBatch, symbol string, want signal.CandidateOutcome) {
	t.Helper()
	for _, evaluation := range batch.Evaluations {
		if evaluation.Symbol == symbol {
			if evaluation.Outcome != want {
				t.Fatalf("%s outcome=%s want=%s evaluation=%#v", symbol, evaluation.Outcome, want, evaluation)
			}
			return
		}
	}
	t.Fatalf("evaluation %s not found", symbol)
}
