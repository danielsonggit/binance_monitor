package candidateanalysis

import (
	"context"
	"time"

	"binance-monitor/internal/domain/market"
)

const AnalysisVersion1 = "candidate-analysis-v1"

type FeatureObservation struct {
	Symbol              string
	Sector              market.Sector
	AsOf                time.Time
	Return15m           float64
	Return1h            float64
	Previous15mAsOf     time.Time
	PreviousReturn15m   *float64
	RecentQuoteVolume1h float64
	QuoteVolume24h      float64
}

type KlineObservation struct {
	Symbol      string
	Sector      market.Sector
	ClosedAt    time.Time
	High        float64
	Close       float64
	QuoteVolume float64
}

type Source interface {
	LatestFeatureAsOf(context.Context, string) (time.Time, error)
	FeatureObservations(context.Context, time.Time, time.Time, string) ([]FeatureObservation, error)
	KlineObservations(context.Context, time.Time, time.Time) ([]KlineObservation, error)
}

type Options struct {
	End            time.Time
	Lookback       time.Duration
	FeatureVersion string
}

type Distribution struct {
	Count int     `json:"count"`
	Min   float64 `json:"min"`
	P25   float64 `json:"p25"`
	P50   float64 `json:"p50"`
	P75   float64 `json:"p75"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
	Mean  float64 `json:"mean"`
}

type CandidateSimulation struct {
	Return15mAbsoluteFloor float64      `json:"return_15m_absolute_floor_percent"`
	Return1hAbsoluteFloor  float64      `json:"return_1h_absolute_floor_percent"`
	Return15mPercentile    float64      `json:"return_15m_cross_sectional_percentile"`
	Return1hPercentile     float64      `json:"return_1h_cross_sectional_percentile"`
	CountPerWindow         Distribution `json:"count_per_window"`
	TurnoverPerComparison  Distribution `json:"turnover_per_comparison_percent"`
	UniqueSymbols          int          `json:"unique_symbols"`
	Entries                int          `json:"entries"`
	Exits                  int          `json:"exits"`
	Comparisons            int          `json:"comparisons"`
}

type KlineMetrics struct {
	Rows                    int          `json:"rows"`
	VolumeExpansion20Median Distribution `json:"volume_expansion_vs_previous_20_median"`
	DrawdownFrom1hHigh      Distribution `json:"drawdown_from_1h_high_percent"`
	PositiveCloseStreak     Distribution `json:"consecutive_positive_15m_close_intervals"`
}

type SectorReport struct {
	Sector                     market.Sector       `json:"sector"`
	FeatureRows                int                 `json:"feature_rows"`
	FeatureWindows             int                 `json:"feature_windows"`
	Return15m                  Distribution        `json:"return_15m_percent"`
	Return1h                   Distribution        `json:"return_1h_percent"`
	Acceleration15m            Distribution        `json:"return_15m_acceleration_percent_points"`
	RecentQuoteVolume1h        Distribution        `json:"recent_quote_volume_1h_usd"`
	QuoteVolume24h             Distribution        `json:"quote_volume_24h_usd"`
	PositiveBreadth15m         Distribution        `json:"positive_breadth_15m_percent"`
	PositiveBreadth1h          Distribution        `json:"positive_breadth_1h_percent"`
	CrossSectionalP95Return15m Distribution        `json:"cross_sectional_p95_return_15m_percent"`
	CrossSectionalP90Return1h  Distribution        `json:"cross_sectional_p90_return_1h_percent"`
	Candidates                 CandidateSimulation `json:"candidate_simulation"`
	Klines                     KlineMetrics        `json:"closed_kline_metrics"`
}

type Report struct {
	AnalysisVersion string         `json:"analysis_version"`
	FeatureVersion  string         `json:"feature_version"`
	Start           time.Time      `json:"start"`
	End             time.Time      `json:"end"`
	GeneratedAt     time.Time      `json:"generated_at"`
	Sectors         []SectorReport `json:"sectors"`
	Notes           []string       `json:"notes"`
}
