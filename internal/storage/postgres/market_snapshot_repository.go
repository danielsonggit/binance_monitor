package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"binance-monitor/internal/domain/market"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MarketSnapshotRepository struct {
	pool         *pgxpool.Pool
	availability market.AvailabilityRule
}

func NewMarketSnapshotRepository(pool *pgxpool.Pool) *MarketSnapshotRepository {
	return NewMarketSnapshotRepositoryWithAvailabilityRule(
		pool,
		market.NewBinanceUSDMAvailabilityClassifier(),
	)
}

func NewMarketSnapshotRepositoryWithAvailabilityRule(
	pool *pgxpool.Pool,
	rule market.AvailabilityRule,
) *MarketSnapshotRepository {
	return &MarketSnapshotRepository{pool: pool, availability: rule}
}

func (r *MarketSnapshotRepository) Save(
	ctx context.Context,
	snapshot market.SnapshotBatch,
) (market.SnapshotWriteResult, error) {
	if err := snapshot.Validate(market.SnapshotInterval); err != nil {
		return market.SnapshotWriteResult{}, err
	}
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return market.SnapshotWriteResult{}, fmt.Errorf("开始 snapshot 事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	active, err := activeSnapshotInstruments(ctx, transaction, snapshot.BucketEnd)
	if err != nil {
		return market.SnapshotWriteResult{}, err
	}
	runID, existing, err := beginSnapshotRun(ctx, transaction, snapshot, len(active))
	if err != nil {
		return market.SnapshotWriteResult{}, err
	}
	if existing != nil {
		if err := transaction.Commit(ctx); err != nil {
			return market.SnapshotWriteResult{}, fmt.Errorf("提交幂等 snapshot 查询: %w", err)
		}
		return *existing, nil
	}

	itemsBySymbol := make(map[string]market.SnapshotItem, len(snapshot.Items))
	for _, item := range snapshot.Items {
		itemsBySymbol[item.Ticker.Symbol] = item
	}
	observedSymbols := make(map[string]struct{}, len(snapshot.ObservedSymbols))
	for _, symbol := range snapshot.ObservedSymbols {
		observedSymbols[symbol] = struct{}{}
	}
	missingSymbols := make([]string, 0)
	stateSymbols := make(map[market.AvailabilityState][]string)
	observations := make([]market.AvailabilityObservation, 0, len(active))
	classifier := r.availability
	if classifier == nil {
		classifier = market.NewBinanceUSDMAvailabilityClassifier()
	}
	batch := &pgx.Batch{}
	actual := 0
	for _, entry := range active {
		symbol := entry.Instrument.Symbol
		item, fresh := itemsBySymbol[symbol]
		_, observed := observedSymbols[symbol]
		observation := classifier.Classify(market.AvailabilityInput{
			Instrument:      entry.Instrument,
			HasTicker:       observed || fresh,
			TickerFresh:     fresh,
			SourceAvailable: snapshot.SourceAvailable,
		})
		observations = append(observations, observation)
		stateSymbols[observation.State] = append(stateSymbols[observation.State], symbol)
		if !fresh {
			missingSymbols = append(missingSymbols, symbol)
			continue
		}
		actual++
		ticker := item.Ticker
		batch.Queue(`
			INSERT INTO market_snapshots_5m (
				instrument_id, bucket_time, last_price,
				price_change_percent_24h, base_volume_24h, quote_volume_24h,
				source_event_time, received_at, quality_score
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (instrument_id, bucket_time) DO UPDATE SET
				last_price = EXCLUDED.last_price,
				price_change_percent_24h = EXCLUDED.price_change_percent_24h,
				base_volume_24h = EXCLUDED.base_volume_24h,
				quote_volume_24h = EXCLUDED.quote_volume_24h,
				source_event_time = EXCLUDED.source_event_time,
				received_at = EXCLUDED.received_at,
				quality_score = EXCLUDED.quality_score`,
			entry.ID,
			snapshot.BucketStart,
			ticker.LastPrice.String(),
			ticker.PriceChangePercent24h().String(),
			ticker.BaseVolume24h.String(),
			ticker.QuoteVolume24h.String(),
			ticker.EventTime,
			ticker.ReceivedAt,
			item.QualityScore,
		)
	}
	sort.Strings(missingSymbols)
	if actual > 0 {
		results := transaction.SendBatch(ctx, batch)
		for index := 0; index < actual; index++ {
			if _, err := results.Exec(); err != nil {
				_ = results.Close()
				return market.SnapshotWriteResult{}, fmt.Errorf("批量写入 snapshot: %w", err)
			}
		}
		if err := results.Close(); err != nil {
			return market.SnapshotWriteResult{}, fmt.Errorf("结束 snapshot batch: %w", err)
		}
	}

	coverage := market.NewSnapshotCoverage(classifier.RuleVersion(), observations)
	if err := coverage.Validate(); err != nil {
		return market.SnapshotWriteResult{}, fmt.Errorf("校验 snapshot coverage: %w", err)
	}
	result := market.SnapshotWriteResult{
		Expected:       len(active),
		Actual:         actual,
		Missing:        len(active) - actual,
		MissingSymbols: missingSymbols,
		Status:         "SUCCEEDED",
		Coverage:       coverage,
	}
	if coverage.AdjustedMissing > 0 || coverage.AdjustedExpected == 0 {
		result.Status = "DEGRADED"
	}
	metadata, err := json.Marshal(snapshotRunMetadata{
		MissingSymbols:  missingSymbols,
		BucketSemantics: "[start,end)",
		Coverage:        coverage,
		StateSymbols:    stateSymbols,
	})
	if err != nil {
		return market.SnapshotWriteResult{}, fmt.Errorf("编码 snapshot metadata: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE collection_runs
		SET actual_count = $2,
			missing_count = $3,
			status = $4,
			error_message = $5,
			completed_at = now(),
			metadata = $6
		WHERE id = $1`,
		runID,
		result.Actual,
		result.Missing,
		result.Status,
		snapshotErrorMessage(result),
		metadata,
	); err != nil {
		return market.SnapshotWriteResult{}, fmt.Errorf("完成 snapshot collection run: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return market.SnapshotWriteResult{}, fmt.Errorf("提交 snapshot 事务: %w", err)
	}
	return result, nil
}

func (r *MarketSnapshotRepository) LoadRecent(ctx context.Context, since time.Time) ([]market.PricePoint, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.symbol,
			s.bucket_time + interval '5 minutes' AS observed_at,
			s.last_price,
			s.source_event_time,
			s.received_at,
			s.quality_score
		FROM market_snapshots_5m s
		JOIN instruments i ON i.id = s.instrument_id
		WHERE s.bucket_time >= $1
		ORDER BY i.symbol, s.bucket_time`, since.UTC().Add(-market.SnapshotInterval))
	if err != nil {
		return nil, fmt.Errorf("查询 snapshot 预热数据: %w", err)
	}
	defer rows.Close()
	points := make([]market.PricePoint, 0)
	for rows.Next() {
		var point market.PricePoint
		var sourceEvent pgtype.Timestamptz
		if err := rows.Scan(
			&point.Symbol,
			&point.ObservedAt,
			&point.Price,
			&sourceEvent,
			&point.ReceivedAt,
			&point.QualityScore,
		); err != nil {
			return nil, fmt.Errorf("读取 snapshot 预热数据: %w", err)
		}
		if sourceEvent.Valid {
			point.SourceEventTime = sourceEvent.Time
		}
		if !point.ObservedAt.Before(since.UTC()) {
			points = append(points, point)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 snapshot 预热数据: %w", err)
	}
	return points, nil
}

// MarkGaps records closed five-minute windows that have no collection run.
// It starts from the latest known run, so a brand-new deployment does not
// manufacture historical gaps for time before the service existed.
func (r *MarketSnapshotRepository) MarkGaps(ctx context.Context, until time.Time) (int, error) {
	until = until.UTC().Truncate(market.SnapshotInterval)
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("开始 snapshot gap 事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	var latestEnd pgtype.Timestamptz
	if err := transaction.QueryRow(ctx, `
		SELECT max(window_end)
		FROM collection_runs
		WHERE job_type = $1 AND status <> 'RUNNING'`, market.SnapshotJobType).Scan(&latestEnd); err != nil {
		return 0, fmt.Errorf("查询最近 snapshot run: %w", err)
	}
	if !latestEnd.Valid {
		if err := transaction.Commit(ctx); err != nil {
			return 0, fmt.Errorf("提交空 snapshot gap 检查: %w", err)
		}
		return 0, nil
	}

	inserted := 0
	for bucketStart := latestEnd.Time.UTC(); !bucketStart.Add(market.SnapshotInterval).After(until); bucketStart = bucketStart.Add(market.SnapshotInterval) {
		bucketEnd := bucketStart.Add(market.SnapshotInterval)
		expected, err := activeInstrumentCount(ctx, transaction, bucketEnd)
		if err != nil {
			return 0, err
		}
		if expected == 0 {
			continue
		}
		result, err := transaction.Exec(ctx, `
			INSERT INTO collection_runs (
				idempotency_key, job_type, window_start, window_end,
				expected_count, actual_count, missing_count, status,
				error_message, completed_at, metadata
			) VALUES ($1, $2, $3, $4, $5, 0, $5, 'DEGRADED',
				'worker 未采集该窗口，无法从 miniTicker 精确回补', now(),
				'{"gap_reason":"collector_not_running"}'::jsonb)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			snapshotRunKey(bucketStart),
			market.SnapshotJobType,
			bucketStart,
			bucketEnd,
			expected,
		)
		if err != nil {
			return 0, fmt.Errorf("登记 snapshot gap %s: %w", bucketStart, err)
		}
		inserted += int(result.RowsAffected())
	}
	if err := transaction.Commit(ctx); err != nil {
		return 0, fmt.Errorf("提交 snapshot gap 事务: %w", err)
	}
	return inserted, nil
}

type activeSnapshotInstrument struct {
	ID         int64
	Instrument market.Instrument
}

func activeSnapshotInstruments(ctx context.Context, transaction pgx.Tx, at time.Time) ([]activeSnapshotInstrument, error) {
	rows, err := transaction.Query(ctx, `
		SELECT id, symbol, sector, exchange_status
		FROM instruments
		WHERE valid_from <= $1
			AND (valid_to IS NULL OR valid_to > $1)
		ORDER BY symbol`, at)
	if err != nil {
		return nil, fmt.Errorf("查询 snapshot 有效合约: %w", err)
	}
	defer rows.Close()
	result := make([]activeSnapshotInstrument, 0)
	for rows.Next() {
		var entry activeSnapshotInstrument
		if err := rows.Scan(
			&entry.ID,
			&entry.Instrument.Symbol,
			&entry.Instrument.Sector,
			&entry.Instrument.ExchangeStatus,
		); err != nil {
			return nil, fmt.Errorf("读取 snapshot 有效合约: %w", err)
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 snapshot 有效合约: %w", err)
	}
	return result, nil
}

func activeInstrumentIDs(ctx context.Context, transaction pgx.Tx, at time.Time) (map[string]int64, error) {
	rows, err := transaction.Query(ctx, `
		SELECT symbol, id
		FROM instruments
		WHERE valid_from <= $1
			AND (valid_to IS NULL OR valid_to > $1)
			AND exchange_status = 'TRADING'`, at)
	if err != nil {
		return nil, fmt.Errorf("查询可交易合约: %w", err)
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var symbol string
		var id int64
		if err := rows.Scan(&symbol, &id); err != nil {
			return nil, fmt.Errorf("读取可交易合约: %w", err)
		}
		result[symbol] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历可交易合约: %w", err)
	}
	return result, nil
}

func activeInstrumentCount(ctx context.Context, transaction pgx.Tx, at time.Time) (int, error) {
	var count int
	if err := transaction.QueryRow(ctx, `
		SELECT count(*)
		FROM instruments
		WHERE valid_from <= $1
			AND (valid_to IS NULL OR valid_to > $1)`, at).Scan(&count); err != nil {
		return 0, fmt.Errorf("统计 snapshot 有效合约: %w", err)
	}
	return count, nil
}

func beginSnapshotRun(
	ctx context.Context,
	transaction pgx.Tx,
	snapshot market.SnapshotBatch,
	expected int,
) (int64, *market.SnapshotWriteResult, error) {
	var runID int64
	err := transaction.QueryRow(ctx, `
		INSERT INTO collection_runs (
			idempotency_key, job_type, window_start, window_end,
			expected_count, actual_count, missing_count, status
		) VALUES ($1, $2, $3, $4, $5, 0, 0, 'RUNNING')
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		snapshotRunKey(snapshot.BucketStart),
		market.SnapshotJobType,
		snapshot.BucketStart,
		snapshot.BucketEnd,
		expected,
	).Scan(&runID)
	if err == nil {
		return runID, nil, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, nil, fmt.Errorf("创建 snapshot collection run: %w", err)
	}
	var existing market.SnapshotWriteResult
	var metadata []byte
	if err := transaction.QueryRow(ctx, `
		SELECT expected_count, actual_count, missing_count, status, metadata
		FROM collection_runs
		WHERE idempotency_key = $1`, snapshotRunKey(snapshot.BucketStart)).Scan(
		&existing.Expected,
		&existing.Actual,
		&existing.Missing,
		&existing.Status,
		&metadata,
	); err != nil {
		return 0, nil, fmt.Errorf("读取已有 snapshot collection run: %w", err)
	}
	var details struct {
		MissingSymbols []string                `json:"missing_symbols"`
		Coverage       market.SnapshotCoverage `json:"coverage"`
	}
	_ = json.Unmarshal(metadata, &details)
	existing.MissingSymbols = details.MissingSymbols
	existing.Coverage = details.Coverage
	existing.AlreadyApplied = true
	return 0, &existing, nil
}

func snapshotRunKey(bucketStart time.Time) string {
	return fmt.Sprintf("market-snapshot-5m:%d", bucketStart.UTC().Unix())
}

func snapshotErrorMessage(result market.SnapshotWriteResult) any {
	if result.Status == "SUCCEEDED" {
		return nil
	}
	return fmt.Sprintf(
		"原始缺少 %d/%d；会话调整后缺少 %d/%d（规则 %s）",
		result.Missing, result.Expected,
		result.Coverage.AdjustedMissing, result.Coverage.AdjustedExpected,
		result.Coverage.RuleVersion,
	)
}

type snapshotRunMetadata struct {
	MissingSymbols  []string                              `json:"missing_symbols"`
	BucketSemantics string                                `json:"bucket_semantics"`
	Coverage        market.SnapshotCoverage               `json:"coverage"`
	StateSymbols    map[market.AvailabilityState][]string `json:"state_symbols"`
}
