package marketquery

import (
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/domain/signal"
	"github.com/shopspring/decimal"
)

type ReturnMetric struct {
	Horizon       market.ReturnHorizon `json:"horizon"`
	ReturnPercent *decimal.Decimal     `json:"return_percent"`
	IsValid       bool                 `json:"is_valid"`
	InvalidReason string               `json:"invalid_reason,omitempty"`
}

type RankingItem struct {
	Rank           int                                   `json:"rank"`
	Symbol         string                                `json:"symbol"`
	BaseAsset      string                                `json:"base_asset"`
	Sector         market.Sector                         `json:"sector"`
	CurrentPrice   decimal.Decimal                       `json:"current_price"`
	QuoteVolume24h decimal.Decimal                       `json:"quote_volume_24h"`
	ReturnPercent  decimal.Decimal                       `json:"return_percent"`
	Percentile     decimal.Decimal                       `json:"percentile"`
	Returns        map[market.ReturnHorizon]ReturnMetric `json:"returns"`
}

type Ranking struct {
	AsOf           time.Time            `json:"as_of"`
	RankingVersion string               `json:"ranking_version"`
	FeatureVersion string               `json:"feature_version"`
	Sector         market.Sector        `json:"sector"`
	Horizon        market.ReturnHorizon `json:"horizon"`
	RequestedLimit int                  `json:"requested_limit"`
	ActiveCount    int                  `json:"active_count"`
	EligibleCount  int                  `json:"eligible_count"`
	PositiveCount  int                  `json:"positive_count"`
	RankedCount    int                  `json:"ranked_count"`
	Items          []RankingItem        `json:"items"`
}

type Feature struct {
	AsOf                time.Time                             `json:"as_of"`
	FeatureVersion      string                                `json:"feature_version"`
	Symbol              string                                `json:"symbol"`
	BaseAsset           string                                `json:"base_asset"`
	Sector              market.Sector                         `json:"sector"`
	CurrentPrice        *decimal.Decimal                      `json:"current_price"`
	CurrentPriceAt      *time.Time                            `json:"current_price_at"`
	CurrentSource       string                                `json:"current_source,omitempty"`
	CurrentAgeSeconds   int64                                 `json:"current_age_seconds"`
	RecentQuoteVolume1h decimal.Decimal                       `json:"recent_quote_volume_1h"`
	QuoteVolume24h      decimal.Decimal                       `json:"quote_volume_24h"`
	Returns             map[market.ReturnHorizon]ReturnMetric `json:"returns"`
	CalculatedAt        time.Time                             `json:"calculated_at"`
}

type BackfillQuality struct {
	Status       string    `json:"status"`
	WindowEnd    time.Time `json:"window_end"`
	MissingCount int       `json:"missing_count"`
	CompletedAt  time.Time `json:"completed_at"`
}

type WorkerQuality struct {
	Status     string         `json:"status"`
	ObservedAt time.Time      `json:"observed_at"`
	Details    map[string]any `json:"details"`
}

type SnapshotQuality struct {
	Status       string                                `json:"status"`
	WindowStart  time.Time                             `json:"window_start"`
	WindowEnd    time.Time                             `json:"window_end"`
	CompletedAt  time.Time                             `json:"completed_at"`
	Coverage     market.SnapshotCoverage               `json:"coverage"`
	StateSymbols map[market.AvailabilityState][]string `json:"state_symbols"`
}

type Quality struct {
	AsOf             time.Time        `json:"as_of"`
	FeatureVersion   string           `json:"feature_version"`
	ActiveSymbols    int              `json:"active_symbols"`
	FeatureRows      int              `json:"feature_rows"`
	ActiveMetrics    int              `json:"active_metrics"`
	ValidMetrics     int              `json:"valid_metrics"`
	InvalidMetrics   int              `json:"invalid_metrics"`
	CoveragePercent  decimal.Decimal  `json:"coverage_percent"`
	InvalidReasons   map[string]int   `json:"invalid_reasons"`
	LastCalculatedAt time.Time        `json:"last_calculated_at"`
	Backfill         *BackfillQuality `json:"backfill,omitempty"`
	Worker           *WorkerQuality   `json:"worker,omitempty"`
	Snapshot         *SnapshotQuality `json:"snapshot,omitempty"`
}

type CandidateItem struct {
	Symbol             string                       `json:"symbol"`
	Sector             market.Sector                `json:"sector"`
	Status             signal.CandidateMemberStatus `json:"status"`
	EnteredAt          time.Time                    `json:"entered_at"`
	LastSelectedAt     time.Time                    `json:"last_selected_at"`
	LastEvaluatedAt    time.Time                    `json:"last_evaluated_at"`
	ConsecutiveMisses  int                          `json:"consecutive_misses"`
	CooldownUntil      *time.Time                   `json:"cooldown_until,omitempty"`
	Availability       market.AvailabilityState     `json:"availability"`
	Return15m          *decimal.Decimal             `json:"return_15m_percent,omitempty"`
	Return1h           *decimal.Decimal             `json:"return_1h_percent,omitempty"`
	Trigger15m         bool                         `json:"trigger_15m"`
	Trigger1h          bool                         `json:"trigger_1h"`
	LiquidityQualified bool                         `json:"liquidity_qualified"`
	Outcome            signal.CandidateOutcome      `json:"outcome"`
	Reasons            []string                     `json:"reasons"`
}

type CandidatePool struct {
	AsOf           time.Time                    `json:"as_of"`
	RuleVersion    string                       `json:"rule_version"`
	FeatureVersion string                       `json:"feature_version"`
	Status         signal.CandidateMemberStatus `json:"status"`
	Sector         market.Sector                `json:"sector,omitempty"`
	Count          int                          `json:"count"`
	Items          []CandidateItem              `json:"items"`
}
