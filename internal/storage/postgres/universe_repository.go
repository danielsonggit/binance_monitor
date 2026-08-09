package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/universe"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UniverseRepository struct {
	pool *pgxpool.Pool
}

func NewUniverseRepository(pool *pgxpool.Pool) *UniverseRepository {
	return &UniverseRepository{pool: pool}
}

func (r *UniverseRepository) ActiveCount(ctx context.Context) (int, error) {
	var count int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM instruments WHERE valid_to IS NULL").Scan(&count); err != nil {
		return 0, fmt.Errorf("统计 active instruments: %w", err)
	}
	return count, nil
}

func (r *UniverseRepository) Reconcile(ctx context.Context, snapshot universe.Snapshot) (universe.Result, error) {
	if snapshot.MissingConfirmations <= 0 {
		return universe.Result{}, fmt.Errorf("MissingConfirmations 必须大于 0")
	}
	transaction, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return universe.Result{}, fmt.Errorf("开始 universe transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	runID, alreadyApplied, err := startUniverseRun(ctx, transaction, snapshot)
	if err != nil {
		return universe.Result{}, err
	}
	if alreadyApplied {
		return universe.Result{AlreadyApplied: true}, nil
	}

	active, err := readActiveInstruments(ctx, transaction)
	if err != nil {
		return universe.Result{}, err
	}
	result := universe.Result{
		Observed:     len(snapshot.Instruments),
		ActiveBefore: len(active),
	}

	observed := append([]market.Instrument(nil), snapshot.Instruments...)
	sort.Slice(observed, func(i, j int) bool { return observed[i].Symbol < observed[j].Symbol })
	seen := make(map[string]struct{}, len(observed))
	for _, instrument := range observed {
		current, exists := active[instrument.Symbol]
		if !exists {
			if err := insertInstrument(ctx, transaction, instrument, snapshot.ObservedAt); err != nil {
				return universe.Result{}, err
			}
			result.Inserted++
			seen[instrument.Symbol] = struct{}{}
			continue
		}
		if current.identityChanged(instrument) {
			if err := closeInstrument(ctx, transaction, current.ID, snapshot.ObservedAt, "REPLACED"); err != nil {
				return universe.Result{}, err
			}
			if err := insertInstrument(ctx, transaction, instrument, snapshot.ObservedAt); err != nil {
				return universe.Result{}, err
			}
			result.Closed++
			result.Inserted++
		} else {
			if err := updateObservedInstrument(ctx, transaction, current.ID, instrument, snapshot.ObservedAt); err != nil {
				return universe.Result{}, err
			}
			result.Updated++
		}
		seen[instrument.Symbol] = struct{}{}
	}

	for symbol, current := range active {
		if _, exists := seen[symbol]; exists {
			continue
		}
		nextMissing := current.MissingObservations + 1
		if nextMissing >= snapshot.MissingConfirmations {
			if err := closeInstrument(ctx, transaction, current.ID, snapshot.ObservedAt, "MISSING"); err != nil {
				return universe.Result{}, err
			}
			result.Closed++
			continue
		}
		if _, err := transaction.Exec(
			ctx,
			"UPDATE instruments SET missing_observations = $2, updated_at = now() WHERE id = $1",
			current.ID,
			nextMissing,
		); err != nil {
			return universe.Result{}, fmt.Errorf("更新 %s missing observation: %w", symbol, err)
		}
		result.MissingPending++
	}

	if err := completeUniverseRun(ctx, transaction, runID, result); err != nil {
		return universe.Result{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return universe.Result{}, fmt.Errorf("提交 universe transaction: %w", err)
	}
	return result, nil
}

type activeInstrument struct {
	ID                  int64
	Symbol              string
	BaseAsset           string
	QuoteAsset          string
	Sector              market.Sector
	ContractType        string
	MissingObservations int
}

func (a activeInstrument) identityChanged(instrument market.Instrument) bool {
	return a.BaseAsset != instrument.BaseAsset ||
		a.QuoteAsset != instrument.QuoteAsset ||
		a.Sector != instrument.Sector ||
		a.ContractType != instrument.ContractType
}

func readActiveInstruments(ctx context.Context, transaction pgx.Tx) (map[string]activeInstrument, error) {
	rows, err := transaction.Query(ctx, `
		SELECT id, symbol, base_asset, quote_asset, sector, contract_type, missing_observations
		FROM instruments
		WHERE valid_to IS NULL
		FOR UPDATE`)
	if err != nil {
		return nil, fmt.Errorf("锁定 active instruments: %w", err)
	}
	defer rows.Close()

	result := make(map[string]activeInstrument)
	for rows.Next() {
		var item activeInstrument
		if err := rows.Scan(
			&item.ID,
			&item.Symbol,
			&item.BaseAsset,
			&item.QuoteAsset,
			&item.Sector,
			&item.ContractType,
			&item.MissingObservations,
		); err != nil {
			return nil, fmt.Errorf("解析 active instrument: %w", err)
		}
		result[item.Symbol] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 active instruments: %w", err)
	}
	return result, nil
}

func startUniverseRun(
	ctx context.Context,
	transaction pgx.Tx,
	snapshot universe.Snapshot,
) (int64, bool, error) {
	idempotencyKey := "universe:" + snapshot.ObservedAt.Format(time.RFC3339)
	var runID int64
	err := transaction.QueryRow(ctx, `
		INSERT INTO collection_runs (
			idempotency_key, job_type, window_start, window_end,
			expected_count, actual_count, missing_count, status
		)
		VALUES ($1, 'UNIVERSE_SYNC', $2, $3, $4, 0, 0, 'RUNNING')
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`,
		idempotencyKey,
		snapshot.ObservedAt,
		snapshot.ObservedAt.Add(time.Minute),
		len(snapshot.Instruments),
	).Scan(&runID)
	if err == pgx.ErrNoRows {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("创建 universe collection run: %w", err)
	}
	return runID, false, nil
}

func completeUniverseRun(
	ctx context.Context,
	transaction pgx.Tx,
	runID int64,
	result universe.Result,
) error {
	metadata, err := json.Marshal(map[string]any{
		"active_before":   result.ActiveBefore,
		"inserted":        result.Inserted,
		"updated":         result.Updated,
		"missing_pending": result.MissingPending,
		"closed":          result.Closed,
	})
	if err != nil {
		return fmt.Errorf("编码 universe run metadata: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE collection_runs
		SET actual_count = $2,
			status = 'SUCCEEDED',
			completed_at = now(),
			metadata = $3
		WHERE id = $1`,
		runID,
		result.Observed,
		metadata,
	); err != nil {
		return fmt.Errorf("完成 universe collection run: %w", err)
	}
	return nil
}

func insertInstrument(ctx context.Context, transaction pgx.Tx, instrument market.Instrument, observedAt time.Time) error {
	metadata, err := instrumentMetadata(instrument)
	if err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO instruments (
			symbol, base_asset, quote_asset, sector, contract_type, status,
			price_precision, quantity_precision, valid_from, last_seen_at, metadata
		)
		VALUES ($1, $2, $3, $4, $5, 'TRADING', $6, $7, $8, $8, $9)`,
		instrument.Symbol,
		instrument.BaseAsset,
		instrument.QuoteAsset,
		instrument.Sector,
		instrument.ContractType,
		instrument.PricePrecision,
		instrument.QuantityPrecision,
		observedAt,
		metadata,
	); err != nil {
		return fmt.Errorf("插入 instrument %s: %w", instrument.Symbol, err)
	}
	return nil
}

func updateObservedInstrument(
	ctx context.Context,
	transaction pgx.Tx,
	id int64,
	instrument market.Instrument,
	observedAt time.Time,
) error {
	metadata, err := instrumentMetadata(instrument)
	if err != nil {
		return err
	}
	if _, err := transaction.Exec(ctx, `
		UPDATE instruments
		SET status = 'TRADING',
			price_precision = $2,
			quantity_precision = $3,
			last_seen_at = $4,
			missing_observations = 0,
			metadata = $5,
			updated_at = now()
		WHERE id = $1`,
		id,
		instrument.PricePrecision,
		instrument.QuantityPrecision,
		observedAt,
		metadata,
	); err != nil {
		return fmt.Errorf("更新 instrument %s: %w", instrument.Symbol, err)
	}
	return nil
}

func closeInstrument(ctx context.Context, transaction pgx.Tx, id int64, observedAt time.Time, status string) error {
	if _, err := transaction.Exec(ctx, `
		UPDATE instruments
		SET status = $3, valid_to = $2, updated_at = now()
		WHERE id = $1 AND valid_to IS NULL`,
		id,
		observedAt,
		status,
	); err != nil {
		return fmt.Errorf("关闭 instrument %d: %w", id, err)
	}
	return nil
}

func instrumentMetadata(instrument market.Instrument) ([]byte, error) {
	encoded, err := json.Marshal(map[string]any{
		"underlying_type":      instrument.UnderlyingType,
		"underlying_sub_types": instrument.UnderlyingSubTypes,
	})
	if err != nil {
		return nil, fmt.Errorf("编码 instrument %s metadata: %w", instrument.Symbol, err)
	}
	return encoded, nil
}
