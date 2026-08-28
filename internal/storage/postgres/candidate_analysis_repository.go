package postgres

import (
	"context"
	"fmt"
	"time"

	"binance-monitor/internal/candidateanalysis"
	"binance-monitor/internal/domain/market"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CandidateAnalysisRepository struct {
	pool *pgxpool.Pool
}

func NewCandidateAnalysisRepository(pool *pgxpool.Pool) *CandidateAnalysisRepository {
	return &CandidateAnalysisRepository{pool: pool}
}

func (r *CandidateAnalysisRepository) LatestFeatureAsOf(ctx context.Context, featureVersion string) (time.Time, error) {
	if r == nil || r.pool == nil {
		return time.Time{}, fmt.Errorf("candidate analysis PostgreSQL pool 不能为空")
	}
	var latest pgtype.Timestamptz
	err := r.pool.QueryRow(ctx, `
		SELECT max(as_of)
		FROM return_feature_snapshots
		WHERE feature_version = $1`, featureVersion).Scan(&latest)
	if err != nil {
		return time.Time{}, fmt.Errorf("查询 candidate analysis 最新 feature 时点: %w", err)
	}
	if !latest.Valid {
		return time.Time{}, fmt.Errorf("feature version %q 没有已落库时点", featureVersion)
	}
	return latest.Time.UTC(), nil
}

func (r *CandidateAnalysisRepository) FeatureObservations(
	ctx context.Context,
	start time.Time,
	end time.Time,
	featureVersion string,
) ([]candidateanalysis.FeatureObservation, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("candidate analysis PostgreSQL pool 不能为空")
	}
	if !start.Before(end) || featureVersion == "" {
		return nil, fmt.Errorf("candidate analysis feature 查询范围无效")
	}
	rows, err := r.pool.Query(ctx, `
		WITH observations AS (
			SELECT
				i.symbol,
				i.sector,
				f.as_of,
				f.return_15m::double precision AS return_15m,
				f.return_1h::double precision AS return_1h,
				f.recent_quote_volume_1h::double precision AS recent_quote_volume_1h,
				f.quote_volume_24h::double precision AS quote_volume_24h,
				lag(f.as_of, 3) OVER (PARTITION BY f.instrument_id ORDER BY f.as_of) AS previous_15m_as_of,
				lag(f.return_15m::double precision, 3) OVER (PARTITION BY f.instrument_id ORDER BY f.as_of) AS previous_return_15m
			FROM return_feature_snapshots f
			JOIN instruments i ON i.id = f.instrument_id
			WHERE f.feature_version = $3
				AND f.as_of >= $1::timestamptz - interval '15 minutes'
				AND f.as_of <= $2::timestamptz
				AND f.is_valid_15m
				AND f.is_valid_1h
				AND i.exchange_status = 'TRADING'
		)
		SELECT symbol, sector, as_of, return_15m, return_1h,
			recent_quote_volume_1h, quote_volume_24h,
			previous_15m_as_of, previous_return_15m
		FROM observations
		WHERE as_of >= $1::timestamptz
		ORDER BY as_of, sector, symbol`, start.UTC(), end.UTC(), featureVersion)
	if err != nil {
		return nil, fmt.Errorf("查询 candidate analysis feature observations: %w", err)
	}
	defer rows.Close()
	result := make([]candidateanalysis.FeatureObservation, 0)
	for rows.Next() {
		var item candidateanalysis.FeatureObservation
		var previousAsOf pgtype.Timestamptz
		var previousReturn pgtype.Float8
		if err := rows.Scan(
			&item.Symbol, &item.Sector, &item.AsOf, &item.Return15m, &item.Return1h,
			&item.RecentQuoteVolume1h, &item.QuoteVolume24h, &previousAsOf, &previousReturn,
		); err != nil {
			return nil, fmt.Errorf("读取 candidate analysis feature observation: %w", err)
		}
		item.AsOf = item.AsOf.UTC()
		if previousAsOf.Valid && previousReturn.Valid {
			value := previousReturn.Float64
			item.Previous15mAsOf = previousAsOf.Time.UTC()
			item.PreviousReturn15m = &value
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 candidate analysis feature observations: %w", err)
	}
	return result, nil
}

func (r *CandidateAnalysisRepository) KlineObservations(
	ctx context.Context,
	start time.Time,
	end time.Time,
) ([]candidateanalysis.KlineObservation, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("candidate analysis PostgreSQL pool 不能为空")
	}
	if !start.Before(end) {
		return nil, fmt.Errorf("candidate analysis K 线查询范围无效")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT i.symbol, i.sector, k.close_time + interval '1 millisecond',
			k.high::double precision, k.close::double precision, k.quote_volume::double precision
		FROM klines_15m k
		JOIN instruments i ON i.id = k.instrument_id
		WHERE k.close_time + interval '1 millisecond' >= $1
			AND k.close_time + interval '1 millisecond' <= $2
			AND i.exchange_status = 'TRADING'
		ORDER BY i.symbol, k.close_time`, start.UTC(), end.UTC())
	if err != nil {
		return nil, fmt.Errorf("查询 candidate analysis K 线 observations: %w", err)
	}
	defer rows.Close()
	result := make([]candidateanalysis.KlineObservation, 0)
	for rows.Next() {
		var item candidateanalysis.KlineObservation
		if err := rows.Scan(
			&item.Symbol, &item.Sector, &item.ClosedAt, &item.High, &item.Close, &item.QuoteVolume,
		); err != nil {
			return nil, fmt.Errorf("读取 candidate analysis K 线 observation: %w", err)
		}
		item.ClosedAt = item.ClosedAt.UTC()
		if item.Sector != market.SectorCrypto && item.Sector != market.SectorTradFi {
			return nil, fmt.Errorf("candidate analysis K 线 sector 无效: %s", item.Sector)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 candidate analysis K 线 observations: %w", err)
	}
	return result, nil
}
