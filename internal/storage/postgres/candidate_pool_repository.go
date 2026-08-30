package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"binance-monitor/internal/candidatepool"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/domain/signal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

type CandidatePoolRepository struct {
	pool *pgxpool.Pool
}

func NewCandidatePoolRepository(pool *pgxpool.Pool) *CandidatePoolRepository {
	return &CandidatePoolRepository{pool: pool}
}

func (r *CandidatePoolRepository) LoadCandidateResult(
	ctx context.Context,
	asOf time.Time,
	ruleVersion string,
) (signal.CandidateWriteResult, bool, error) {
	if r == nil || r.pool == nil || asOf.IsZero() || strings.TrimSpace(ruleVersion) == "" {
		return signal.CandidateWriteResult{}, false, fmt.Errorf("candidate pool 幂等查询参数无效")
	}
	key := fmt.Sprintf("candidate-pool:%s:%s", ruleVersion, asOf.UTC().Format(time.RFC3339))
	var metadata []byte
	err := r.pool.QueryRow(ctx, `SELECT metadata FROM collection_runs WHERE idempotency_key = $1`, key).Scan(&metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return signal.CandidateWriteResult{}, false, nil
	}
	if err != nil {
		return signal.CandidateWriteResult{}, false, fmt.Errorf("查询 candidate pool 幂等记录: %w", err)
	}
	result, err := decodeCandidateResult(metadata)
	if err != nil {
		return signal.CandidateWriteResult{}, false, err
	}
	return result, true, nil
}

func (r *CandidatePoolRepository) LoadCandidateInputs(
	ctx context.Context,
	asOf time.Time,
	featureVersion string,
) ([]signal.CandidateInput, error) {
	if r == nil || r.pool == nil {
		return nil, fmt.Errorf("candidate pool PostgreSQL pool 不能为空")
	}
	asOf = asOf.UTC()
	if asOf.IsZero() || !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) || strings.TrimSpace(featureVersion) == "" {
		return nil, fmt.Errorf("candidate pool 输入查询参数无效")
	}
	var snapshotMetadata []byte
	err := r.pool.QueryRow(ctx, `
		SELECT metadata
		FROM collection_runs
		WHERE job_type = $1 AND window_end = $2 AND status <> 'RUNNING'
		ORDER BY completed_at DESC NULLS LAST
		LIMIT 1`, market.SnapshotJobType, asOf).Scan(&snapshotMetadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("candidate pool %s 缺少同事件时点 snapshot quality", asOf.Format(time.RFC3339))
	}
	if err != nil {
		return nil, fmt.Errorf("查询 candidate pool snapshot quality: %w", err)
	}
	states, err := decodeAvailabilityStates(snapshotMetadata)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT i.id, i.symbol, i.sector,
			f.return_15m, f.return_1h,
			COALESCE(f.is_valid_15m, false), COALESCE(f.is_valid_1h, false),
			COALESCE(f.recent_quote_volume_1h, 0), COALESCE(f.quote_volume_24h, 0)
		FROM instruments i
		LEFT JOIN return_feature_snapshots f
			ON f.instrument_id = i.id AND f.as_of = $1 AND f.feature_version = $2
		WHERE i.valid_from <= $1 AND (i.valid_to IS NULL OR i.valid_to > $1)
		ORDER BY i.sector, i.symbol`, asOf, featureVersion)
	if err != nil {
		return nil, fmt.Errorf("查询 candidate pool feature inputs: %w", err)
	}
	defer rows.Close()
	result := make([]signal.CandidateInput, 0)
	for rows.Next() {
		var item signal.CandidateInput
		var return15m, return1h decimal.NullDecimal
		if err := rows.Scan(
			&item.InstrumentID, &item.Symbol, &item.Sector, &return15m, &return1h, &item.Valid15m, &item.Valid1h,
			&item.RecentQuoteVolume1h, &item.QuoteVolume24h,
		); err != nil {
			return nil, fmt.Errorf("读取 candidate pool feature input: %w", err)
		}
		item.Availability = states[item.Symbol]
		if item.Availability == "" {
			item.Availability = market.AvailabilityUnknown
		}
		if item.Valid15m {
			item.Return15m = return15m.Decimal
		}
		if item.Valid1h {
			item.Return1h = return1h.Decimal
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 candidate pool feature inputs: %w", err)
	}
	return result, nil
}

func decodeAvailabilityStates(metadata []byte) (map[string]market.AvailabilityState, error) {
	var details struct {
		StateSymbols map[market.AvailabilityState][]string `json:"state_symbols"`
	}
	if err := json.Unmarshal(metadata, &details); err != nil {
		return nil, fmt.Errorf("解析 candidate pool snapshot states: %w", err)
	}
	if len(details.StateSymbols) == 0 {
		return nil, fmt.Errorf("candidate pool snapshot 缺少 state_symbols")
	}
	result := make(map[string]market.AvailabilityState)
	for state, symbols := range details.StateSymbols {
		for _, symbol := range symbols {
			if previous, exists := result[symbol]; exists {
				return nil, fmt.Errorf("candidate pool symbol %s 同时属于 %s/%s", symbol, previous, state)
			}
			result[symbol] = state
		}
	}
	return result, nil
}

func (r *CandidatePoolRepository) LoadCandidateMembers(ctx context.Context, ruleVersion string) ([]signal.CandidateMember, error) {
	if r == nil || r.pool == nil || strings.TrimSpace(ruleVersion) == "" {
		return nil, fmt.Errorf("candidate pool member 查询参数无效")
	}
	rows, err := r.pool.Query(ctx, `
		SELECT m.instrument_id, i.symbol, i.sector, m.direction, m.rule_version, m.status,
			m.entered_at, m.last_selected_at, m.last_evaluated_at,
			m.consecutive_misses, m.cooldown_until
		FROM candidate_pool_members m
		JOIN instruments i ON i.id = m.instrument_id
		WHERE m.rule_version = $1
		ORDER BY i.symbol`, ruleVersion)
	if err != nil {
		return nil, fmt.Errorf("查询 candidate pool members: %w", err)
	}
	defer rows.Close()
	result := make([]signal.CandidateMember, 0)
	for rows.Next() {
		var item signal.CandidateMember
		var cooldown pgtype.Timestamptz
		if err := rows.Scan(
			&item.InstrumentID, &item.Symbol, &item.Sector, &item.Direction, &item.RuleVersion, &item.Status,
			&item.EnteredAt, &item.LastSelectedAt, &item.LastEvaluatedAt,
			&item.ConsecutiveMisses, &cooldown,
		); err != nil {
			return nil, fmt.Errorf("读取 candidate pool member: %w", err)
		}
		item.EnteredAt = item.EnteredAt.UTC()
		item.LastSelectedAt = item.LastSelectedAt.UTC()
		item.LastEvaluatedAt = item.LastEvaluatedAt.UTC()
		if cooldown.Valid {
			item.CooldownUntil = cooldown.Time.UTC()
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 candidate pool members: %w", err)
	}
	return result, nil
}

func (r *CandidatePoolRepository) SaveCandidateBatch(
	ctx context.Context,
	batch signal.CandidateBatch,
) (signal.CandidateWriteResult, error) {
	if r == nil || r.pool == nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("candidate pool PostgreSQL pool 不能为空")
	}
	if err := batch.Validate(); err != nil {
		return signal.CandidateWriteResult{}, err
	}
	transaction, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("开始 candidate pool 事务: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"candidate-pool:"+batch.Rules.RuleVersion); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("锁定 candidate pool: %w", err)
	}
	idempotencyKey := fmt.Sprintf("candidate-pool:%s:%s", batch.Rules.RuleVersion, batch.AsOf.UTC().Format(time.RFC3339))
	if existing, found, err := loadExistingCandidateResult(ctx, transaction, idempotencyKey); err != nil {
		return signal.CandidateWriteResult{}, err
	} else if found {
		existing.AlreadyApplied = true
		return existing, nil
	}
	var latest pgtype.Timestamptz
	if err := transaction.QueryRow(ctx, `
		SELECT max(window_end) FROM collection_runs
		WHERE job_type = $1 AND metadata->>'rule_version' = $2`,
		candidatepool.JobType, batch.Rules.RuleVersion).Scan(&latest); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("查询 candidate pool 最新时点: %w", err)
	}
	if latest.Valid && !latest.Time.Before(batch.AsOf) {
		return signal.CandidateWriteResult{}, fmt.Errorf("candidate pool 拒绝乱序时点 %s，最新为 %s", batch.AsOf.Format(time.RFC3339), latest.Time.Format(time.RFC3339))
	}
	configJSON, err := batch.Rules.CanonicalJSON()
	if err != nil {
		return signal.CandidateWriteResult{}, err
	}
	checksum, err := batch.Rules.Checksum()
	if err != nil {
		return signal.CandidateWriteResult{}, err
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO candidate_rule_versions (
			rule_version, feature_version, direction, config_json, checksum_sha256, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (rule_version) DO NOTHING`, batch.Rules.RuleVersion, batch.Rules.FeatureVersion,
		batch.Rules.Direction, configJSON, checksum, batch.CalculatedAt.UTC()); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("写入 candidate rule version: %w", err)
	}
	var storedChecksum string
	if err := transaction.QueryRow(ctx, `SELECT checksum_sha256 FROM candidate_rule_versions WHERE rule_version = $1`,
		batch.Rules.RuleVersion).Scan(&storedChecksum); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("校验 candidate rule version: %w", err)
	}
	if storedChecksum != checksum {
		return signal.CandidateWriteResult{}, fmt.Errorf("candidate rule version %s 内容冲突", batch.Rules.RuleVersion)
	}
	instrumentIDs := make([]int64, 0, len(batch.Evaluations))
	for _, evaluation := range batch.Evaluations {
		instrumentIDs = append(instrumentIDs, evaluation.InstrumentID)
	}
	var existingInstruments int
	if err := transaction.QueryRow(ctx, `SELECT count(*) FROM instruments WHERE id = ANY($1)`, instrumentIDs).Scan(&existingInstruments); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("校验 candidate instruments: %w", err)
	}
	if existingInstruments != len(batch.Evaluations) {
		return signal.CandidateWriteResult{}, fmt.Errorf("candidate evaluations 包含不存在的 instrument：existing=%d evaluations=%d", existingInstruments, len(batch.Evaluations))
	}

	writeBatch := &pgx.Batch{}
	for _, evaluation := range batch.Evaluations {
		if evaluation.Outcome == "" {
			return signal.CandidateWriteResult{}, fmt.Errorf("candidate evaluation %s 无效或 instrument 不存在", evaluation.Symbol)
		}
		reasons, err := json.Marshal(evaluation.Reasons)
		if err != nil {
			return signal.CandidateWriteResult{}, fmt.Errorf("编码 candidate evaluation reasons: %w", err)
		}
		writeBatch.Queue(`
			INSERT INTO candidate_evaluations (
				instrument_id, as_of, rule_version, feature_version, sector, direction, availability_state,
				return_15m, return_1h, is_valid_15m, is_valid_1h,
				percentile_15m, percentile_1h, threshold_15m, threshold_1h,
				recent_quote_volume_1h, quote_volume_24h, trigger_15m, trigger_1h,
				liquidity_qualified, priority_ratio, capacity_rank, prior_status, outcome,
				consecutive_misses, cooldown_until, reasons_json, calculated_at
			) VALUES (
				$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28
			)`, evaluation.InstrumentID, batch.AsOf.UTC(), batch.Rules.RuleVersion, batch.Rules.FeatureVersion,
			evaluation.Sector, batch.Rules.Direction, evaluation.Availability,
			nullableCandidateReturn(evaluation.Return15m, evaluation.Valid15m),
			nullableCandidateReturn(evaluation.Return1h, evaluation.Valid1h),
			evaluation.Valid15m, evaluation.Valid1h, evaluation.Percentile15m.String(), evaluation.Percentile1h.String(),
			evaluation.Threshold15m.String(), evaluation.Threshold1h.String(), evaluation.RecentQuoteVolume1h.String(),
			evaluation.QuoteVolume24h.String(), evaluation.Trigger15m, evaluation.Trigger1h, evaluation.LiquidityQualified,
			evaluation.PriorityRatio.String(), evaluation.CapacityRank, nullableMemberStatus(evaluation.PriorStatus), evaluation.Outcome,
			evaluation.ConsecutiveMisses, nullableTime(evaluation.CooldownUntil), reasons, batch.CalculatedAt.UTC())
	}
	for _, member := range batch.Members {
		writeBatch.Queue(`
			INSERT INTO candidate_pool_members (
				instrument_id, direction, rule_version, status, entered_at, last_selected_at,
				last_evaluated_at, consecutive_misses, cooldown_until, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
			ON CONFLICT (instrument_id, direction, rule_version) DO UPDATE SET
				status = EXCLUDED.status,
				entered_at = EXCLUDED.entered_at,
				last_selected_at = EXCLUDED.last_selected_at,
				last_evaluated_at = EXCLUDED.last_evaluated_at,
				consecutive_misses = EXCLUDED.consecutive_misses,
				cooldown_until = EXCLUDED.cooldown_until,
				updated_at = EXCLUDED.updated_at`, member.InstrumentID, member.Direction, member.RuleVersion, member.Status,
			member.EnteredAt.UTC(), member.LastSelectedAt.UTC(), member.LastEvaluatedAt.UTC(), member.ConsecutiveMisses,
			nullableTime(member.CooldownUntil), batch.CalculatedAt.UTC())
	}
	results := transaction.SendBatch(ctx, writeBatch)
	for range len(batch.Evaluations) + len(batch.Members) {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return signal.CandidateWriteResult{}, fmt.Errorf("批量写入 candidate pool: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("结束 candidate pool batch: %w", err)
	}
	result := summarizeCandidateBatch(batch)
	metadata, err := json.Marshal(map[string]any{
		"rule_version": batch.Rules.RuleVersion, "feature_version": batch.Rules.FeatureVersion,
		"rule_checksum": checksum, "result": result,
	})
	if err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("编码 candidate pool run metadata: %w", err)
	}
	if _, err := transaction.Exec(ctx, `
		INSERT INTO collection_runs (
			idempotency_key, job_type, window_start, window_end, expected_count,
			actual_count, missing_count, status, completed_at, metadata
		) VALUES ($1,$2,$3,$4,$5,$5,0,'SUCCEEDED',$6,$7)`,
		idempotencyKey, candidatepool.JobType, batch.AsOf.Add(-market.SnapshotInterval), batch.AsOf,
		len(batch.Evaluations), batch.CalculatedAt.UTC(), metadata); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("记录 candidate pool collection run: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("提交 candidate pool 事务: %w", err)
	}
	return result, nil
}

func loadExistingCandidateResult(ctx context.Context, transaction pgx.Tx, key string) (signal.CandidateWriteResult, bool, error) {
	var metadata []byte
	err := transaction.QueryRow(ctx, `SELECT metadata FROM collection_runs WHERE idempotency_key = $1`, key).Scan(&metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return signal.CandidateWriteResult{}, false, nil
	}
	if err != nil {
		return signal.CandidateWriteResult{}, false, fmt.Errorf("查询 candidate pool 幂等记录: %w", err)
	}
	result, err := decodeCandidateResult(metadata)
	return result, true, err
}

func decodeCandidateResult(metadata []byte) (signal.CandidateWriteResult, error) {
	var wrapper struct {
		Result signal.CandidateWriteResult `json:"result"`
	}
	if err := json.Unmarshal(metadata, &wrapper); err != nil {
		return signal.CandidateWriteResult{}, fmt.Errorf("解析 candidate pool 幂等记录: %w", err)
	}
	return wrapper.Result, nil
}

func summarizeCandidateBatch(batch signal.CandidateBatch) signal.CandidateWriteResult {
	result := signal.CandidateWriteResult{AsOf: batch.AsOf.UTC(), Evaluated: len(batch.Evaluations)}
	for _, member := range batch.Members {
		if member.Status == signal.CandidateMemberActive {
			result.Active++
		}
	}
	for _, evaluation := range batch.Evaluations {
		switch evaluation.Outcome {
		case signal.CandidateEntered:
			result.Entered++
		case signal.CandidateContinued:
			result.Continued++
		case signal.CandidateMissHeld:
			result.Held++
		case signal.CandidateExited:
			result.Exited++
		case signal.CandidateRejectedQuality:
			result.RejectedQuality++
		case signal.CandidateRejectedMomentum:
			result.RejectedMomentum++
		case signal.CandidateRejectedLiquidity:
			result.RejectedLiquidity++
		case signal.CandidateRejectedCapacity:
			result.RejectedCapacity++
		case signal.CandidateRejectedCooldown:
			result.RejectedCooldown++
		}
	}
	return result
}

func nullableCandidateReturn(value interface{ String() string }, valid bool) any {
	if !valid {
		return nil
	}
	return value.String()
}

func nullableMemberStatus(status signal.CandidateMemberStatus) any {
	if status == "" {
		return nil
	}
	return status
}
