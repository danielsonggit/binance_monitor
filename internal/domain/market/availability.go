package market

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

const BinanceUSDMAvailabilityRuleV1 = "binance-usdm-availability-v1"

type ExchangeStatus string

const (
	ExchangeStatusTrading        ExchangeStatus = "TRADING"
	ExchangeStatusPendingTrading ExchangeStatus = "PENDING_TRADING"
	ExchangeStatusSettling       ExchangeStatus = "SETTLING"
)

type AvailabilityState string

const (
	AvailabilityOpen              AvailabilityState = "OPEN"
	AvailabilityMarketClosed      AvailabilityState = "MARKET_CLOSED"
	AvailabilityLowActivity       AvailabilityState = "LOW_ACTIVITY"
	AvailabilityDataMissing       AvailabilityState = "DATA_MISSING"
	AvailabilitySourceUnavailable AvailabilityState = "SOURCE_UNAVAILABLE"
	AvailabilityUnknown           AvailabilityState = "UNKNOWN"
)

type AvailabilityObservation struct {
	Symbol         string            `json:"symbol"`
	Sector         Sector            `json:"sector"`
	ExchangeStatus ExchangeStatus    `json:"exchange_status"`
	TickerObserved bool              `json:"ticker_observed"`
	TickerFresh    bool              `json:"ticker_fresh"`
	State          AvailabilityState `json:"state"`
	Reason         string            `json:"reason"`
}

type AvailabilityInput struct {
	Instrument      Instrument
	HasTicker       bool
	TickerFresh     bool
	SourceAvailable bool
}

type AvailabilityClassifier struct {
	ruleVersion string
}

type AvailabilityRule interface {
	RuleVersion() string
	Classify(AvailabilityInput) AvailabilityObservation
}

func NewBinanceUSDMAvailabilityClassifier() AvailabilityClassifier {
	return AvailabilityClassifier{ruleVersion: BinanceUSDMAvailabilityRuleV1}
}

func (c AvailabilityClassifier) RuleVersion() string {
	return c.ruleVersion
}

func (c AvailabilityClassifier) Classify(input AvailabilityInput) AvailabilityObservation {
	status := input.Instrument.NormalizedExchangeStatus()
	result := AvailabilityObservation{
		Symbol:         input.Instrument.Symbol,
		Sector:         input.Instrument.Sector,
		ExchangeStatus: status,
		TickerObserved: input.HasTicker,
		TickerFresh:    input.HasTicker && input.TickerFresh,
	}
	if !input.SourceAvailable {
		result.State = AvailabilitySourceUnavailable
		result.Reason = "GLOBAL_MARKET_SOURCE_UNAVAILABLE"
		return result
	}
	switch status {
	case ExchangeStatusPendingTrading, ExchangeStatusSettling:
		result.State = AvailabilityMarketClosed
		result.Reason = "BINANCE_EXCHANGE_STATUS_" + string(status)
		return result
	case ExchangeStatusTrading:
		// Continue with per-symbol freshness below.
	default:
		result.State = AvailabilityUnknown
		result.Reason = "UNKNOWN_BINANCE_EXCHANGE_STATUS"
		return result
	}
	if input.HasTicker && input.TickerFresh {
		result.State = AvailabilityOpen
		result.Reason = "FRESH_MINI_TICKER"
		return result
	}
	if input.HasTicker {
		result.State = AvailabilityLowActivity
		result.Reason = "MINI_TICKER_PRESENT_BUT_STALE"
		return result
	}
	result.State = AvailabilityDataMissing
	result.Reason = "MINI_TICKER_NOT_OBSERVED"
	return result
}

type SnapshotCoverage struct {
	RuleVersion             string                    `json:"rule_version"`
	RawExpected             int                       `json:"raw_expected"`
	RawActual               int                       `json:"raw_actual"`
	RawMissing              int                       `json:"raw_missing"`
	RawCoveragePercent      decimal.Decimal           `json:"raw_coverage_percent"`
	AdjustedExpected        int                       `json:"adjusted_expected"`
	AdjustedActual          int                       `json:"adjusted_actual"`
	AdjustedMissing         int                       `json:"adjusted_missing"`
	AdjustedCoveragePercent decimal.Decimal           `json:"adjusted_coverage_percent"`
	StateCounts             map[AvailabilityState]int `json:"state_counts"`
}

func NewSnapshotCoverage(ruleVersion string, observations []AvailabilityObservation) SnapshotCoverage {
	result := SnapshotCoverage{
		RuleVersion: strings.TrimSpace(ruleVersion),
		RawExpected: len(observations),
		StateCounts: map[AvailabilityState]int{
			AvailabilityOpen: 0, AvailabilityMarketClosed: 0, AvailabilityLowActivity: 0,
			AvailabilityDataMissing: 0, AvailabilitySourceUnavailable: 0, AvailabilityUnknown: 0,
		},
	}
	for _, observation := range observations {
		result.StateCounts[observation.State]++
		if observation.TickerFresh {
			result.RawActual++
		}
		if observation.State == AvailabilityMarketClosed {
			continue
		}
		result.AdjustedExpected++
		if observation.State == AvailabilityOpen {
			result.AdjustedActual++
		}
	}
	result.RawMissing = result.RawExpected - result.RawActual
	result.AdjustedMissing = result.AdjustedExpected - result.AdjustedActual
	result.RawCoveragePercent = coveragePercent(result.RawActual, result.RawExpected)
	result.AdjustedCoveragePercent = coveragePercent(result.AdjustedActual, result.AdjustedExpected)
	return result
}

func (c SnapshotCoverage) Validate() error {
	if strings.TrimSpace(c.RuleVersion) == "" {
		return fmt.Errorf("availability rule version 不能为空")
	}
	if c.RawExpected < 0 || c.RawActual < 0 || c.RawMissing < 0 ||
		c.AdjustedExpected < 0 || c.AdjustedActual < 0 || c.AdjustedMissing < 0 {
		return fmt.Errorf("snapshot coverage 计数不能为负数")
	}
	if c.RawActual+c.RawMissing != c.RawExpected {
		return fmt.Errorf("raw snapshot coverage 计数不一致")
	}
	if c.AdjustedActual+c.AdjustedMissing != c.AdjustedExpected {
		return fmt.Errorf("adjusted snapshot coverage 计数不一致")
	}
	if c.AdjustedExpected > c.RawExpected || c.AdjustedActual > c.RawActual {
		return fmt.Errorf("adjusted snapshot coverage 不能大于 raw coverage")
	}
	if c.RawCoveragePercent.IsNegative() || c.RawCoveragePercent.GreaterThan(decimal.NewFromInt(100)) ||
		c.AdjustedCoveragePercent.IsNegative() || c.AdjustedCoveragePercent.GreaterThan(decimal.NewFromInt(100)) {
		return fmt.Errorf("snapshot coverage 百分比必须在 0 到 100 之间")
	}
	return nil
}

func (c SnapshotCoverage) Healthy(minimumPercent int) bool {
	return minimumPercent > 0 && minimumPercent <= 100 && c.AdjustedExpected > 0 &&
		c.AdjustedActual*100 >= c.AdjustedExpected*minimumPercent
}

func coveragePercent(actual, expected int) decimal.Decimal {
	if expected <= 0 {
		return decimal.Zero
	}
	return decimal.NewFromInt(int64(actual)).
		Div(decimal.NewFromInt(int64(expected))).
		Mul(decimal.NewFromInt(100)).
		Round(6)
}
