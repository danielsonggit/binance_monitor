package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/marketquery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type MarketQueryRepository struct {
	pool *pgxpool.Pool
}

func NewMarketQueryRepository(pool *pgxpool.Pool) *MarketQueryRepository {
	return &MarketQueryRepository{pool: pool}
}

func (r *MarketQueryRepository) LatestRanking(
	ctx context.Context,
	sector market.Sector,
	horizon market.ReturnHorizon,
	limit int,
) (marketquery.Ranking, error) {
	if r == nil || r.pool == nil {
		return marketquery.Ranking{}, fmt.Errorf("market query PostgreSQL pool 不能为空")
	}
	var result marketquery.Ranking
	err := r.pool.QueryRow(ctx, `
		SELECT as_of, ranking_version, feature_version, sector, horizon,
			requested_limit, active_count, eligible_count, positive_count, ranked_count
		FROM ranking_snapshots
		WHERE sector = $1 AND horizon = $2
			AND ranking_version = $3 AND feature_version = $4
		ORDER BY as_of DESC, calculated_at DESC
		LIMIT 1`, sector, horizon, market.RankingVersion1, market.ReturnFeatureVersion1).Scan(
		&result.AsOf, &result.RankingVersion, &result.FeatureVersion, &result.Sector, &result.Horizon,
		&result.RequestedLimit, &result.ActiveCount, &result.EligibleCount, &result.PositiveCount, &result.RankedCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return marketquery.Ranking{}, marketquery.ErrNotFound
	}
	if err != nil {
		return marketquery.Ranking{}, fmt.Errorf("查询最新 ranking snapshot: %w", err)
	}
	result.AsOf = result.AsOf.UTC()
	rows, err := r.pool.Query(ctx, `
		SELECT
			ri.rank_position, i.symbol, i.base_asset, i.sector,
			ri.current_price, ri.quote_volume_24h, ri.return_percent, ri.percentile,
			COALESCE(f.return_15m, 0), COALESCE(f.is_valid_15m, false), COALESCE(f.quality_json->'15m'->>'invalid_reason', ''),
			COALESCE(f.return_1h, 0), COALESCE(f.is_valid_1h, false), COALESCE(f.quality_json->'1h'->>'invalid_reason', ''),
			COALESCE(f.return_4h, 0), COALESCE(f.is_valid_4h, false), COALESCE(f.quality_json->'4h'->>'invalid_reason', ''),
			COALESCE(f.return_24h, 0), COALESCE(f.is_valid_24h, false), COALESCE(f.quality_json->'24h'->>'invalid_reason', '')
		FROM ranking_snapshots s
		JOIN ranking_snapshot_items ri ON ri.ranking_snapshot_id = s.id AND ri.as_of = s.as_of
		JOIN instruments i ON i.id = ri.instrument_id
		JOIN return_feature_snapshots f
			ON f.instrument_id = ri.instrument_id
			AND f.as_of = s.as_of
			AND f.feature_version = s.feature_version
		WHERE s.as_of = $1 AND s.ranking_version = $2 AND s.feature_version = $3
			AND s.sector = $4 AND s.horizon = $5
		ORDER BY ri.rank_position
		LIMIT $6`, result.AsOf, result.RankingVersion, result.FeatureVersion, sector, horizon, limit)
	if err != nil {
		return marketquery.Ranking{}, fmt.Errorf("查询 ranking items: %w", err)
	}
	defer rows.Close()
	result.Items = make([]marketquery.RankingItem, 0, min(limit, result.RankedCount))
	for rows.Next() {
		var item marketquery.RankingItem
		metrics := emptyQueryMetrics()
		if err := rows.Scan(
			&item.Rank, &item.Symbol, &item.BaseAsset, &item.Sector,
			&item.CurrentPrice, &item.QuoteVolume24h, &item.ReturnPercent, &item.Percentile,
			&metrics[0].value, &metrics[0].valid, &metrics[0].reason,
			&metrics[1].value, &metrics[1].valid, &metrics[1].reason,
			&metrics[2].value, &metrics[2].valid, &metrics[2].reason,
			&metrics[3].value, &metrics[3].valid, &metrics[3].reason,
		); err != nil {
			return marketquery.Ranking{}, fmt.Errorf("读取 ranking item: %w", err)
		}
		item.Returns = buildQueryMetrics(metrics)
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return marketquery.Ranking{}, fmt.Errorf("遍历 ranking items: %w", err)
	}
	return result, nil
}

func (r *MarketQueryRepository) LatestFeature(ctx context.Context, symbol string) (marketquery.Feature, error) {
	if r == nil || r.pool == nil {
		return marketquery.Feature{}, fmt.Errorf("market query PostgreSQL pool 不能为空")
	}
	var result marketquery.Feature
	var currentPrice decimal.Decimal
	var currentPriceAt time.Time
	metrics := emptyQueryMetrics()
	err := r.pool.QueryRow(ctx, `
		SELECT
			f.as_of, f.feature_version, i.symbol, i.base_asset, i.sector,
			COALESCE(f.current_price, 0), COALESCE(f.current_price_at, 'epoch'::timestamptz),
			COALESCE(f.current_source, ''), f.current_age_seconds,
			f.recent_quote_volume_1h, f.quote_volume_24h,
			COALESCE(f.return_15m, 0), f.is_valid_15m, COALESCE(f.quality_json->'15m'->>'invalid_reason', ''),
			COALESCE(f.return_1h, 0), f.is_valid_1h, COALESCE(f.quality_json->'1h'->>'invalid_reason', ''),
			COALESCE(f.return_4h, 0), f.is_valid_4h, COALESCE(f.quality_json->'4h'->>'invalid_reason', ''),
			COALESCE(f.return_24h, 0), f.is_valid_24h, COALESCE(f.quality_json->'24h'->>'invalid_reason', ''),
			f.calculated_at
		FROM return_feature_snapshots f
		JOIN instruments i ON i.id = f.instrument_id
		WHERE i.symbol = $1 AND f.feature_version = $2
		ORDER BY f.as_of DESC, f.calculated_at DESC
		LIMIT 1`, symbol, market.ReturnFeatureVersion1).Scan(
		&result.AsOf, &result.FeatureVersion, &result.Symbol, &result.BaseAsset, &result.Sector,
		&currentPrice, &currentPriceAt, &result.CurrentSource, &result.CurrentAgeSeconds,
		&result.RecentQuoteVolume1h, &result.QuoteVolume24h,
		&metrics[0].value, &metrics[0].valid, &metrics[0].reason,
		&metrics[1].value, &metrics[1].valid, &metrics[1].reason,
		&metrics[2].value, &metrics[2].valid, &metrics[2].reason,
		&metrics[3].value, &metrics[3].valid, &metrics[3].reason,
		&result.CalculatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return marketquery.Feature{}, marketquery.ErrNotFound
	}
	if err != nil {
		return marketquery.Feature{}, fmt.Errorf("查询最新 symbol feature: %w", err)
	}
	result.AsOf = result.AsOf.UTC()
	result.CalculatedAt = result.CalculatedAt.UTC()
	if currentPrice.IsPositive() {
		result.CurrentPrice = decimalPointer(currentPrice)
		value := currentPriceAt.UTC()
		result.CurrentPriceAt = &value
	}
	result.Returns = buildQueryMetrics(metrics)
	return result, nil
}

func (r *MarketQueryRepository) LatestQuality(ctx context.Context) (marketquery.Quality, error) {
	if r == nil || r.pool == nil {
		return marketquery.Quality{}, fmt.Errorf("market query PostgreSQL pool 不能为空")
	}
	var result marketquery.Quality
	err := r.pool.QueryRow(ctx, `
		SELECT as_of, feature_version, count(*),
			sum(is_valid_15m::int + is_valid_1h::int + is_valid_4h::int + is_valid_24h::int),
			max(calculated_at)
		FROM return_feature_snapshots
		WHERE feature_version = $1
		GROUP BY as_of, feature_version
		ORDER BY as_of DESC
		LIMIT 1`, market.ReturnFeatureVersion1).Scan(
		&result.AsOf, &result.FeatureVersion, &result.FeatureRows, &result.ValidMetrics, &result.LastCalculatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return marketquery.Quality{}, marketquery.ErrNotFound
	}
	if err != nil {
		return marketquery.Quality{}, fmt.Errorf("查询最新 feature quality: %w", err)
	}
	result.AsOf = result.AsOf.UTC()
	result.LastCalculatedAt = result.LastCalculatedAt.UTC()
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM instruments
		WHERE valid_from <= $1 AND (valid_to IS NULL OR valid_to > $1)
			AND exchange_status = 'TRADING'`, result.AsOf).Scan(&result.ActiveSymbols); err != nil {
		return marketquery.Quality{}, fmt.Errorf("查询 quality active symbols: %w", err)
	}
	result.ActiveMetrics = result.ActiveSymbols * len(market.ReturnHorizons())
	result.InvalidMetrics = result.ActiveMetrics - result.ValidMetrics
	if result.ActiveMetrics > 0 {
		result.CoveragePercent = decimal.NewFromInt(int64(result.ValidMetrics)).
			Div(decimal.NewFromInt(int64(result.ActiveMetrics))).
			Mul(decimal.NewFromInt(100)).Round(6)
	}
	result.InvalidReasons = make(map[string]int)
	var reasonsJSON []byte
	err = r.pool.QueryRow(ctx, `
		SELECT COALESCE(metadata->'invalid_reasons', '{}'::jsonb)
		FROM collection_runs
		WHERE job_type = 'RETURN_FEATURES_5M' AND window_end = $1
		ORDER BY completed_at DESC NULLS LAST
		LIMIT 1`, result.AsOf).Scan(&reasonsJSON)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return marketquery.Quality{}, fmt.Errorf("查询 feature invalid reasons: %w", err)
	}
	if len(reasonsJSON) > 0 {
		if err := json.Unmarshal(reasonsJSON, &result.InvalidReasons); err != nil {
			return marketquery.Quality{}, fmt.Errorf("解析 feature invalid reasons: %w", err)
		}
	}
	result.Backfill, err = r.latestBackfillQuality(ctx)
	if err != nil {
		return marketquery.Quality{}, err
	}
	result.Worker, err = r.workerQuality(ctx)
	if err != nil {
		return marketquery.Quality{}, err
	}
	result.Snapshot, err = r.SnapshotQuality(ctx, time.Time{})
	if err != nil {
		return marketquery.Quality{}, err
	}
	return result, nil
}

func (r *MarketQueryRepository) SnapshotQuality(ctx context.Context, asOf time.Time) (*marketquery.SnapshotQuality, error) {
	var result marketquery.SnapshotQuality
	var metadata []byte
	var expected, actual, missing int
	query := `
		SELECT status, window_start, window_end, COALESCE(completed_at, started_at),
			expected_count, actual_count, missing_count, metadata
		FROM collection_runs
		WHERE job_type = $1 AND status <> 'RUNNING'
		ORDER BY window_end DESC, completed_at DESC NULLS LAST
		LIMIT 1`
	arguments := []any{market.SnapshotJobType}
	if !asOf.IsZero() {
		query = `
			SELECT status, window_start, window_end, COALESCE(completed_at, started_at),
				expected_count, actual_count, missing_count, metadata
			FROM collection_runs
			WHERE job_type = $1 AND status <> 'RUNNING' AND window_end = $2
			ORDER BY completed_at DESC NULLS LAST
			LIMIT 1`
		arguments = append(arguments, asOf.UTC())
	}
	err := r.pool.QueryRow(ctx, query, arguments...).Scan(
		&result.Status, &result.WindowStart, &result.WindowEnd, &result.CompletedAt,
		&expected, &actual, &missing, &metadata,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询最新 snapshot quality: %w", err)
	}
	var details struct {
		Coverage     market.SnapshotCoverage               `json:"coverage"`
		StateSymbols map[market.AvailabilityState][]string `json:"state_symbols"`
	}
	if len(metadata) > 0 {
		if err := json.Unmarshal(metadata, &details); err != nil {
			return nil, fmt.Errorf("解析 snapshot quality metadata: %w", err)
		}
	}
	result.Coverage = details.Coverage
	result.Coverage.EnsureOperationalCoverage()
	result.StateSymbols = details.StateSymbols
	if result.Coverage.RuleVersion == "" {
		result.Coverage = market.SnapshotCoverage{
			RuleVersion:                "legacy-raw-coverage",
			RawExpected:                expected,
			RawActual:                  actual,
			RawMissing:                 missing,
			AdjustedExpected:           expected,
			AdjustedActual:             actual,
			AdjustedMissing:            missing,
			RawCoveragePercent:         snapshotCoveragePercent(actual, expected),
			AdjustedCoveragePercent:    snapshotCoveragePercent(actual, expected),
			OperationalExpected:        expected,
			OperationalActual:          actual,
			OperationalMissing:         missing,
			OperationalCoveragePercent: snapshotCoveragePercent(actual, expected),
		}
	}
	result.WindowStart = result.WindowStart.UTC()
	result.WindowEnd = result.WindowEnd.UTC()
	result.CompletedAt = result.CompletedAt.UTC()
	return &result, nil
}

func snapshotCoveragePercent(actual, expected int) decimal.Decimal {
	if expected <= 0 {
		return decimal.Zero
	}
	return decimal.NewFromInt(int64(actual)).Div(decimal.NewFromInt(int64(expected))).
		Mul(decimal.NewFromInt(100)).Round(6)
}

func (r *MarketQueryRepository) latestBackfillQuality(ctx context.Context) (*marketquery.BackfillQuality, error) {
	var result marketquery.BackfillQuality
	err := r.pool.QueryRow(ctx, `
		SELECT status, window_end, missing_count, COALESCE(completed_at, started_at)
		FROM collection_runs
		WHERE job_type = 'KLINE_15M_BACKFILL'
		ORDER BY completed_at DESC NULLS LAST, started_at DESC
		LIMIT 1`).Scan(&result.Status, &result.WindowEnd, &result.MissingCount, &result.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询最新 backfill quality: %w", err)
	}
	result.WindowEnd = result.WindowEnd.UTC()
	result.CompletedAt = result.CompletedAt.UTC()
	return &result, nil
}

func (r *MarketQueryRepository) workerQuality(ctx context.Context) (*marketquery.WorkerQuality, error) {
	var result marketquery.WorkerQuality
	var details []byte
	err := r.pool.QueryRow(ctx, `
		SELECT status, observed_at, detail_json
		FROM system_heartbeats
		WHERE component = 'v2-worker'`).Scan(&result.Status, &result.ObservedAt, &details)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询 worker quality: %w", err)
	}
	result.ObservedAt = result.ObservedAt.UTC()
	if err := json.Unmarshal(details, &result.Details); err != nil {
		return nil, fmt.Errorf("解析 worker heartbeat details: %w", err)
	}
	return &result, nil
}

type queryMetric struct {
	horizon market.ReturnHorizon
	value   decimal.Decimal
	valid   bool
	reason  string
}

func emptyQueryMetrics() []queryMetric {
	return []queryMetric{
		{horizon: market.ReturnHorizon15m},
		{horizon: market.ReturnHorizon1h},
		{horizon: market.ReturnHorizon4h},
		{horizon: market.ReturnHorizon24h},
	}
}

func buildQueryMetrics(values []queryMetric) map[market.ReturnHorizon]marketquery.ReturnMetric {
	result := make(map[market.ReturnHorizon]marketquery.ReturnMetric, len(values))
	for _, value := range values {
		metric := marketquery.ReturnMetric{
			Horizon: value.horizon, IsValid: value.valid, InvalidReason: value.reason,
		}
		if value.valid {
			metric.ReturnPercent = decimalPointer(value.value)
		}
		result[value.horizon] = metric
	}
	return result
}

func decimalPointer(value decimal.Decimal) *decimal.Decimal {
	copy := value
	return &copy
}
