package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"binance-monitor/internal/domain/market"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type KlineRepository struct {
	pool *pgxpool.Pool
}

func NewKlineRepository(pool *pgxpool.Pool) *KlineRepository {
	return &KlineRepository{pool: pool}
}

func (r *KlineRepository) Save(
	ctx context.Context,
	batch market.KlineBatch,
) (market.KlineWriteResult, error) {
	if err := batch.Validate(); err != nil {
		return market.KlineWriteResult{}, err
	}
	if r == nil || r.pool == nil {
		return market.KlineWriteResult{}, fmt.Errorf("K 线 PostgreSQL pool 不能为空")
	}

	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return market.KlineWriteResult{}, fmt.Errorf("开始 K 线事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	symbols := uniqueSortedKlineSymbols(batch.Items)
	// Historical candles are attached to the currently active instrument row.
	// valid_from records when this service first observed the contract, not the
	// exchange listing time, so filtering it by candle open_time would reject a
	// legitimate initial backfill.
	instrumentIDs, err := activeKlineInstrumentIDs(ctx, transaction, symbols)
	if err != nil {
		return market.KlineWriteResult{}, err
	}
	missing := make([]string, 0)
	for _, symbol := range symbols {
		if _, exists := instrumentIDs[symbol]; !exists {
			missing = append(missing, symbol)
		}
	}
	if len(missing) > 0 {
		return market.KlineWriteResult{}, fmt.Errorf(
			"K 线对应的 active instruments 不存在：%s",
			strings.Join(missing, ","),
		)
	}

	pgxBatch := &pgx.Batch{}
	for _, item := range batch.Items {
		pgxBatch.Queue(`
			INSERT INTO klines_15m (
				instrument_id, open_time, close_time,
				open, high, low, close,
				volume, quote_volume, trade_count,
				taker_buy_base_volume, taker_buy_quote_volume,
				source, received_at
			) VALUES (
				$1, $2, $3,
				$4, $5, $6, $7,
				$8, $9, $10,
				$11, $12,
				$13, $14
			)
			ON CONFLICT (instrument_id, open_time) DO UPDATE SET
				close_time = EXCLUDED.close_time,
				open = EXCLUDED.open,
				high = EXCLUDED.high,
				low = EXCLUDED.low,
				close = EXCLUDED.close,
				volume = EXCLUDED.volume,
				quote_volume = EXCLUDED.quote_volume,
				trade_count = EXCLUDED.trade_count,
				taker_buy_base_volume = EXCLUDED.taker_buy_base_volume,
				taker_buy_quote_volume = EXCLUDED.taker_buy_quote_volume,
				source = EXCLUDED.source,
				received_at = EXCLUDED.received_at`,
			instrumentIDs[item.Symbol],
			item.OpenTime.UTC(),
			item.CloseTime.UTC(),
			item.Open.String(),
			item.High.String(),
			item.Low.String(),
			item.Close.String(),
			item.Volume.String(),
			item.QuoteVolume.String(),
			item.TradeCount,
			item.TakerBuyBaseVolume.String(),
			item.TakerBuyQuoteVolume.String(),
			batch.Source,
			batch.ReceivedAt.UTC(),
		)
	}

	results := transaction.SendBatch(ctx, pgxBatch)
	upserted := 0
	for range batch.Items {
		commandTag, err := results.Exec()
		if err != nil {
			_ = results.Close()
			return market.KlineWriteResult{}, fmt.Errorf("批量写入 K 线: %w", err)
		}
		upserted += int(commandTag.RowsAffected())
	}
	if err := results.Close(); err != nil {
		return market.KlineWriteResult{}, fmt.Errorf("结束 K 线 batch: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return market.KlineWriteResult{}, fmt.Errorf("提交 K 线事务: %w", err)
	}
	return market.KlineWriteResult{Attempted: len(batch.Items), Upserted: upserted}, nil
}

func uniqueSortedKlineSymbols(items []market.Kline) []string {
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		seen[item.Symbol] = struct{}{}
	}
	symbols := make([]string, 0, len(seen))
	for symbol := range seen {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	return symbols
}

func activeKlineInstrumentIDs(
	ctx context.Context,
	transaction pgx.Tx,
	symbols []string,
) (map[string]int64, error) {
	rows, err := transaction.Query(ctx, `
		SELECT symbol, id
		FROM instruments
		WHERE valid_to IS NULL AND symbol = ANY($1)`, symbols)
	if err != nil {
		return nil, fmt.Errorf("查询 K 线 active instruments: %w", err)
	}
	defer rows.Close()

	result := make(map[string]int64, len(symbols))
	for rows.Next() {
		var symbol string
		var id int64
		if err := rows.Scan(&symbol, &id); err != nil {
			return nil, fmt.Errorf("读取 K 线 instrument: %w", err)
		}
		result[symbol] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 K 线 instruments: %w", err)
	}
	return result, nil
}
