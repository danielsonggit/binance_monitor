package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const rankingJobType = "RANKINGS_5M"

type RankingRepository struct {
	pool *pgxpool.Pool
}

func NewRankingRepository(pool *pgxpool.Pool) *RankingRepository {
	return &RankingRepository{pool: pool}
}

func (r *RankingRepository) LoadRankingInputs(
	ctx context.Context,
	asOf time.Time,
	featureVersion string,
) ([]market.RankingInput, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("ranking PostgreSQL pool 不能为空")
	}
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) || featureVersion == "" {
		return nil, fmt.Errorf("ranking 查询参数无效")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT
			i.symbol,
			i.sector,
			COALESCE(f.current_price, 0),
			COALESCE(f.quote_volume_24h, 0),
			COALESCE(f.return_15m, 0), COALESCE(f.is_valid_15m, false),
			COALESCE(f.return_1h, 0), COALESCE(f.is_valid_1h, false),
			COALESCE(f.return_4h, 0), COALESCE(f.is_valid_4h, false),
			COALESCE(f.return_24h, 0), COALESCE(f.is_valid_24h, false)
		FROM instruments i
		LEFT JOIN return_feature_snapshots f
			ON f.instrument_id = i.id
			AND f.as_of = $1
			AND f.feature_version = $2
		WHERE i.valid_from <= $1
			AND (i.valid_to IS NULL OR i.valid_to > $1)
			AND i.exchange_status = 'TRADING'
		ORDER BY i.symbol`, asOf, featureVersion)
	if err != nil {
		return nil, fmt.Errorf("查询 ranking inputs: %w", err)
	}
	defer rows.Close()
	inputs := make([]market.RankingInput, 0)
	for rows.Next() {
		input := market.RankingInput{Metrics: make(map[market.ReturnHorizon]market.RankingMetricInput, len(market.ReturnHorizons()))}
		metrics := []market.RankingMetricInput{
			{Horizon: market.ReturnHorizon15m},
			{Horizon: market.ReturnHorizon1h},
			{Horizon: market.ReturnHorizon4h},
			{Horizon: market.ReturnHorizon24h},
		}
		if err := rows.Scan(
			&input.Symbol, &input.Sector, &input.CurrentPrice, &input.QuoteVolume24h,
			&metrics[0].ReturnPercent, &metrics[0].IsValid,
			&metrics[1].ReturnPercent, &metrics[1].IsValid,
			&metrics[2].ReturnPercent, &metrics[2].IsValid,
			&metrics[3].ReturnPercent, &metrics[3].IsValid,
		); err != nil {
			return nil, fmt.Errorf("读取 ranking input: %w", err)
		}
		for _, metric := range metrics {
			input.Metrics[metric.Horizon] = metric
		}
		inputs = append(inputs, input)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 ranking inputs: %w", err)
	}
	return inputs, nil
}

func (r *RankingRepository) SaveRankings(
	ctx context.Context,
	batch market.RankingBatch,
) (market.RankingWriteResult, error) {
	if err := batch.Validate(); err != nil {
		return market.RankingWriteResult{}, err
	}
	if r == nil || r.pool == nil {
		return market.RankingWriteResult{}, fmt.Errorf("ranking PostgreSQL pool 不能为空")
	}
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return market.RankingWriteResult{}, fmt.Errorf("开始 ranking 事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	active, err := loadActiveRankingInstruments(ctx, transaction, batch.AsOf)
	if err != nil {
		return market.RankingWriteResult{}, err
	}
	activeCounts := map[market.Sector]int{market.SectorCrypto: 0, market.SectorTradFi: 0}
	for _, item := range active {
		activeCounts[item.sector]++
	}

	result := market.RankingWriteResult{}
	totalActive := 0
	totalEligible := 0
	totalPositive := 0
	totalRanked := 0
	for _, group := range batch.Groups {
		if group.ActiveCount != activeCounts[group.Sector] {
			return market.RankingWriteResult{}, fmt.Errorf(
				"ranking %s/%s active count 不一致：database=%d batch=%d",
				group.Sector, group.Horizon, activeCounts[group.Sector], group.ActiveCount,
			)
		}
		var snapshotID int64
		if err := transaction.QueryRow(ctx, `
			INSERT INTO ranking_snapshots (
				as_of, ranking_version, feature_version, sector, horizon,
				requested_limit, active_count, eligible_count, positive_count, ranked_count, calculated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (as_of, ranking_version, feature_version, sector, horizon) DO UPDATE SET
				requested_limit = EXCLUDED.requested_limit,
				active_count = EXCLUDED.active_count,
				eligible_count = EXCLUDED.eligible_count,
				positive_count = EXCLUDED.positive_count,
				ranked_count = EXCLUDED.ranked_count,
				calculated_at = EXCLUDED.calculated_at
			RETURNING id`,
			batch.AsOf.UTC(), batch.RankingVersion, batch.FeatureVersion, group.Sector, group.Horizon,
			group.RequestedLimit, group.ActiveCount, group.EligibleCount, group.PositiveCount, len(group.Items), batch.CalculatedAt.UTC(),
		).Scan(&snapshotID); err != nil {
			return market.RankingWriteResult{}, fmt.Errorf("写入 ranking snapshot %s/%s: %w", group.Sector, group.Horizon, err)
		}
		if _, err := transaction.Exec(ctx,
			"DELETE FROM ranking_snapshot_items WHERE ranking_snapshot_id = $1 AND as_of = $2",
			snapshotID, batch.AsOf.UTC(),
		); err != nil {
			return market.RankingWriteResult{}, fmt.Errorf("清理旧 ranking items %s/%s: %w", group.Sector, group.Horizon, err)
		}
		for _, item := range group.Items {
			instrument, exists := active[item.Symbol]
			if !exists || instrument.sector != group.Sector {
				return market.RankingWriteResult{}, fmt.Errorf("ranking item %s 不属于 active %s", item.Symbol, group.Sector)
			}
			if _, err := transaction.Exec(ctx, `
				INSERT INTO ranking_snapshot_items (
					ranking_snapshot_id, as_of, rank_position, instrument_id,
					return_percent, current_price, quote_volume_24h, percentile
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				snapshotID, batch.AsOf.UTC(), item.Rank, instrument.id,
				item.ReturnPercent.String(), item.CurrentPrice.String(), item.QuoteVolume24h.String(), item.Percentile.String(),
			); err != nil {
				return market.RankingWriteResult{}, fmt.Errorf("写入 ranking item %s/%s/%d: %w", group.Sector, group.Horizon, item.Rank, err)
			}
			result.ItemsWritten++
		}
		result.GroupsUpserted++
		totalActive += group.ActiveCount
		totalEligible += group.EligibleCount
		totalPositive += group.PositiveCount
		totalRanked += len(group.Items)
	}
	metadata, err := json.Marshal(map[string]any{
		"ranking_version":  batch.RankingVersion,
		"feature_version":  batch.FeatureVersion,
		"groups":           len(batch.Groups),
		"positive_metrics": totalPositive,
		"ranked_items":     totalRanked,
	})
	if err != nil {
		return market.RankingWriteResult{}, fmt.Errorf("编码 ranking run metadata: %w", err)
	}
	idempotencyKey := fmt.Sprintf("rankings:%s:%s:%s", batch.RankingVersion, batch.FeatureVersion, batch.AsOf.UTC().Format(time.RFC3339))
	if _, err := transaction.Exec(ctx, `
		INSERT INTO collection_runs (
			idempotency_key, job_type, window_start, window_end,
			expected_count, actual_count, missing_count, status,
			completed_at, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, 'SUCCEEDED', now(), $8)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			expected_count = EXCLUDED.expected_count,
			actual_count = EXCLUDED.actual_count,
			missing_count = EXCLUDED.missing_count,
			status = 'SUCCEEDED',
			error_message = NULL,
			started_at = now(),
			completed_at = now(),
			metadata = EXCLUDED.metadata`,
		idempotencyKey, rankingJobType,
		batch.AsOf.Add(-market.SnapshotInterval), batch.AsOf,
		totalActive, totalEligible, totalActive-totalEligible, metadata,
	); err != nil {
		return market.RankingWriteResult{}, fmt.Errorf("记录 ranking collection run: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return market.RankingWriteResult{}, fmt.Errorf("提交 ranking 事务: %w", err)
	}
	return result, nil
}

type activeRankingInstrument struct {
	id     int64
	sector market.Sector
}

func loadActiveRankingInstruments(
	ctx context.Context,
	transaction pgx.Tx,
	asOf time.Time,
) (map[string]activeRankingInstrument, error) {
	rows, err := transaction.Query(ctx, `
		SELECT id, symbol, sector
		FROM instruments
		WHERE valid_from <= $1
			AND (valid_to IS NULL OR valid_to > $1)
			AND exchange_status = 'TRADING'`, asOf.UTC())
	if err != nil {
		return nil, fmt.Errorf("查询 ranking active instruments: %w", err)
	}
	defer rows.Close()
	active := make(map[string]activeRankingInstrument)
	for rows.Next() {
		var id int64
		var symbol string
		var sector market.Sector
		if err := rows.Scan(&id, &symbol, &sector); err != nil {
			return nil, fmt.Errorf("读取 ranking active instrument: %w", err)
		}
		active[symbol] = activeRankingInstrument{id: id, sector: sector}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 ranking active instruments: %w", err)
	}
	return active, nil
}
