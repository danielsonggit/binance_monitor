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

const returnFeatureJobType = "RETURN_FEATURES_5M"

type ReturnFeatureRepository struct {
	pool *pgxpool.Pool
}

func NewReturnFeatureRepository(pool *pgxpool.Pool) *ReturnFeatureRepository {
	return &ReturnFeatureRepository{pool: pool}
}

func (r *ReturnFeatureRepository) LoadReturnInputs(
	ctx context.Context,
	asOf time.Time,
	lookback time.Duration,
) ([]market.ReturnFeatureInput, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("return feature PostgreSQL pool 不能为空")
	}
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) || lookback < 24*time.Hour {
		return nil, fmt.Errorf("return feature 查询时间范围无效")
	}
	start := asOf.Add(-lookback)
	rows, err := r.pool.Query(ctx, `
		SELECT id, symbol, sector
		FROM instruments
		WHERE valid_from <= $1
			AND (valid_to IS NULL OR valid_to > $1)
			AND exchange_status = 'TRADING'
		ORDER BY symbol`, asOf)
	if err != nil {
		return nil, fmt.Errorf("查询 feature active instruments: %w", err)
	}
	inputs := make([]market.ReturnFeatureInput, 0)
	indexByID := make(map[int64]int)
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		var item market.ReturnFeatureInput
		if err := rows.Scan(&id, &item.Symbol, &item.Sector); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取 feature instrument: %w", err)
		}
		inputs = append(inputs, item)
		indexByID[id] = len(inputs) - 1
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("遍历 feature instruments: %w", err)
	}
	rows.Close()
	if len(inputs) == 0 {
		return inputs, nil
	}
	klineRows, err := r.pool.Query(ctx, `
		SELECT instrument_id, close_time + interval '1 millisecond', close, quote_volume, trade_count
		FROM klines_15m
		WHERE instrument_id = ANY($1)
			AND close_time + interval '1 millisecond' >= $2
			AND close_time + interval '1 millisecond' <= $3
		ORDER BY instrument_id, close_time`, ids, start, asOf)
	if err != nil {
		return nil, fmt.Errorf("查询 feature K 线: %w", err)
	}
	for klineRows.Next() {
		var id int64
		var price market.FeaturePricePoint
		var kline market.FeatureKlinePoint
		if err := klineRows.Scan(&id, &price.ObservedAt, &price.Price, &kline.QuoteVolume, &kline.TradeCount); err != nil {
			klineRows.Close()
			return nil, fmt.Errorf("读取 feature K 线: %w", err)
		}
		price.Source = market.PriceSourceKline15m
		price.QualityScore = 100
		kline.CloseAt = price.ObservedAt.UTC()
		price.ObservedAt = price.ObservedAt.UTC()
		input := &inputs[indexByID[id]]
		input.Prices = append(input.Prices, price)
		input.Klines = append(input.Klines, kline)
	}
	if err := klineRows.Err(); err != nil {
		klineRows.Close()
		return nil, fmt.Errorf("遍历 feature K 线: %w", err)
	}
	klineRows.Close()

	snapshotRows, err := r.pool.Query(ctx, `
		SELECT instrument_id, bucket_time + interval '5 minutes', last_price, quality_score
		FROM market_snapshots_5m
		WHERE instrument_id = ANY($1)
			AND bucket_time + interval '5 minutes' >= $2
			AND bucket_time + interval '5 minutes' <= $3
		ORDER BY instrument_id, bucket_time`, ids, start, asOf)
	if err != nil {
		return nil, fmt.Errorf("查询 feature 5 分钟快照: %w", err)
	}
	for snapshotRows.Next() {
		var id int64
		var price market.FeaturePricePoint
		if err := snapshotRows.Scan(&id, &price.ObservedAt, &price.Price, &price.QualityScore); err != nil {
			snapshotRows.Close()
			return nil, fmt.Errorf("读取 feature 5 分钟快照: %w", err)
		}
		price.ObservedAt = price.ObservedAt.UTC()
		price.Source = market.PriceSourceSnapshot5m
		index := indexByID[id]
		inputs[index].Prices = append(inputs[index].Prices, price)
	}
	if err := snapshotRows.Err(); err != nil {
		snapshotRows.Close()
		return nil, fmt.Errorf("遍历 feature 5 分钟快照: %w", err)
	}
	snapshotRows.Close()
	return inputs, nil
}

func (r *ReturnFeatureRepository) SaveReturnFeatures(
	ctx context.Context,
	batch market.ReturnFeatureBatch,
) (market.ReturnFeatureWriteResult, error) {
	if err := batch.Validate(); err != nil {
		return market.ReturnFeatureWriteResult{}, err
	}
	if r == nil || r.pool == nil {
		return market.ReturnFeatureWriteResult{}, fmt.Errorf("return feature PostgreSQL pool 不能为空")
	}
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return market.ReturnFeatureWriteResult{}, fmt.Errorf("开始 return feature 事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	active, err := activeInstrumentIDs(ctx, transaction, batch.AsOf)
	if err != nil {
		return market.ReturnFeatureWriteResult{}, err
	}
	if len(active) != len(batch.Items) {
		return market.ReturnFeatureWriteResult{}, fmt.Errorf(
			"return feature 必须覆盖全部 active instruments：active=%d items=%d", len(active), len(batch.Items),
		)
	}

	invalidReasons := make(map[string]int)
	validMetrics := 0
	pgxBatch := &pgx.Batch{}
	for _, item := range batch.Items {
		instrumentID, exists := active[item.Symbol]
		if !exists {
			return market.ReturnFeatureWriteResult{}, fmt.Errorf("return feature active instrument 不存在：%s", item.Symbol)
		}
		quality, err := encodeReturnQuality(item, invalidReasons, &validMetrics)
		if err != nil {
			return market.ReturnFeatureWriteResult{}, err
		}
		pgxBatch.Queue(`
			INSERT INTO return_feature_snapshots (
				instrument_id, as_of, feature_version,
				current_price, current_price_at, current_source, current_age_seconds,
				recent_quote_volume_1h, quote_volume_24h,
				return_15m, return_1h, return_4h, return_24h,
				is_valid_15m, is_valid_1h, is_valid_4h, is_valid_24h,
				quality_json, calculated_at
			) VALUES (
				$1, $2, $3,
				$4, $5, $6, $7,
				$8, $9,
				$10, $11, $12, $13,
				$14, $15, $16, $17,
				$18, $19
			)
			ON CONFLICT (instrument_id, as_of, feature_version) DO UPDATE SET
				current_price = EXCLUDED.current_price,
				current_price_at = EXCLUDED.current_price_at,
				current_source = EXCLUDED.current_source,
				current_age_seconds = EXCLUDED.current_age_seconds,
				recent_quote_volume_1h = EXCLUDED.recent_quote_volume_1h,
				quote_volume_24h = EXCLUDED.quote_volume_24h,
				return_15m = EXCLUDED.return_15m,
				return_1h = EXCLUDED.return_1h,
				return_4h = EXCLUDED.return_4h,
				return_24h = EXCLUDED.return_24h,
				is_valid_15m = EXCLUDED.is_valid_15m,
				is_valid_1h = EXCLUDED.is_valid_1h,
				is_valid_4h = EXCLUDED.is_valid_4h,
				is_valid_24h = EXCLUDED.is_valid_24h,
				quality_json = EXCLUDED.quality_json,
				calculated_at = EXCLUDED.calculated_at`,
			instrumentID, batch.AsOf.UTC(), batch.FeatureVersion,
			nullableCurrentPrice(item), nullableTime(item.CurrentPriceAt), nullableString(item.CurrentSource), item.CurrentAgeSeconds,
			item.RecentQuoteVolume1h.String(), item.QuoteVolume24h.String(),
			nullableReturn(item.Metrics[market.ReturnHorizon15m]),
			nullableReturn(item.Metrics[market.ReturnHorizon1h]),
			nullableReturn(item.Metrics[market.ReturnHorizon4h]),
			nullableReturn(item.Metrics[market.ReturnHorizon24h]),
			item.Metrics[market.ReturnHorizon15m].IsValid,
			item.Metrics[market.ReturnHorizon1h].IsValid,
			item.Metrics[market.ReturnHorizon4h].IsValid,
			item.Metrics[market.ReturnHorizon24h].IsValid,
			quality, batch.CalculatedAt.UTC(),
		)
	}
	results := transaction.SendBatch(ctx, pgxBatch)
	upserted := 0
	for range batch.Items {
		tag, err := results.Exec()
		if err != nil {
			_ = results.Close()
			return market.ReturnFeatureWriteResult{}, fmt.Errorf("批量写入 return feature: %w", err)
		}
		upserted += int(tag.RowsAffected())
	}
	if err := results.Close(); err != nil {
		return market.ReturnFeatureWriteResult{}, fmt.Errorf("结束 return feature batch: %w", err)
	}
	metadata, err := json.Marshal(map[string]any{
		"feature_version": batch.FeatureVersion,
		"valid_metrics":   validMetrics,
		"invalid_metrics": len(batch.Items)*len(market.ReturnHorizons()) - validMetrics,
		"invalid_reasons": invalidReasons,
	})
	if err != nil {
		return market.ReturnFeatureWriteResult{}, fmt.Errorf("编码 return feature run metadata: %w", err)
	}
	idempotencyKey := fmt.Sprintf("return-features:%s:%s", batch.FeatureVersion, batch.AsOf.UTC().Format(time.RFC3339))
	if _, err := transaction.Exec(ctx, `
		INSERT INTO collection_runs (
			idempotency_key, job_type, window_start, window_end,
			expected_count, actual_count, missing_count, status,
			completed_at, metadata
		) VALUES ($1, $2, $3, $4, $5, $5, 0, 'SUCCEEDED', now(), $6)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			expected_count = EXCLUDED.expected_count,
			actual_count = EXCLUDED.actual_count,
			missing_count = 0,
			status = 'SUCCEEDED',
			error_message = NULL,
			started_at = now(),
			completed_at = now(),
			metadata = EXCLUDED.metadata`,
		idempotencyKey, returnFeatureJobType,
		batch.AsOf.Add(-market.SnapshotInterval), batch.AsOf,
		len(batch.Items), metadata,
	); err != nil {
		return market.ReturnFeatureWriteResult{}, fmt.Errorf("记录 return feature collection run: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return market.ReturnFeatureWriteResult{}, fmt.Errorf("提交 return feature 事务: %w", err)
	}
	return market.ReturnFeatureWriteResult{Attempted: len(batch.Items), Upserted: upserted}, nil
}

func encodeReturnQuality(
	item market.ReturnFeatureSet,
	invalidReasons map[string]int,
	validMetrics *int,
) ([]byte, error) {
	quality := make(map[string]any, len(market.ReturnHorizons()))
	for _, horizon := range market.ReturnHorizons() {
		metric := item.Metrics[horizon]
		entry := map[string]any{
			"target_at":               metric.TargetAt.UTC(),
			"baseline_offset_seconds": metric.BaselineOffsetSeconds,
			"gap_count":               metric.GapCount,
			"is_valid":                metric.IsValid,
			"invalid_reason":          metric.InvalidReason,
		}
		if !metric.BaselinePriceAt.IsZero() {
			entry["baseline_price"] = metric.BaselinePrice.String()
			entry["baseline_price_at"] = metric.BaselinePriceAt.UTC()
			entry["baseline_source"] = metric.BaselineSource
		}
		if metric.IsValid {
			entry["return_percent"] = metric.ReturnPercent.String()
			*validMetrics = *validMetrics + 1
		} else {
			invalidReasons[metric.InvalidReason]++
		}
		quality[string(horizon)] = entry
	}
	encoded, err := json.Marshal(quality)
	if err != nil {
		return nil, fmt.Errorf("编码 %s return feature quality: %w", item.Symbol, err)
	}
	return encoded, nil
}

func nullableCurrentPrice(item market.ReturnFeatureSet) any {
	if item.CurrentPriceAt.IsZero() {
		return nil
	}
	return item.CurrentPrice.String()
}

func nullableReturn(metric market.ReturnMetric) any {
	if !metric.IsValid {
		return nil
	}
	return metric.ReturnPercent.String()
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
