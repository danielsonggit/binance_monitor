package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"binance-monitor/internal/backfill"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BackfillAuditRepository struct {
	pool *pgxpool.Pool
}

func NewBackfillAuditRepository(pool *pgxpool.Pool) *BackfillAuditRepository {
	return &BackfillAuditRepository{pool: pool}
}

type backfillFailureMetadata struct {
	Symbol string    `json:"symbol"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Error  string    `json:"error"`
}

type backfillGapMetadata struct {
	Symbol         string    `json:"symbol"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	ExpectedCount  int       `json:"expected_count"`
	RemainingCount int       `json:"remaining_count"`
	Status         string    `json:"status"`
	LastError      string    `json:"last_error,omitempty"`
}

// Record stores the full gap/failure audit in collection_runs metadata. The
// window is the idempotency boundary, so rerunning the same completed-candle
// window updates the audit instead of creating a duplicate run.
func (r *BackfillAuditRepository) Record(
	ctx context.Context,
	result backfill.Result,
	startedAt time.Time,
	completedAt time.Time,
) error {
	if r == nil || r.pool == nil {
		return fmt.Errorf("backfill audit PostgreSQL pool 不能为空")
	}
	if !result.WindowStart.Before(result.WindowEnd) {
		return fmt.Errorf("backfill audit window 无效")
	}
	if result.Expected < 0 || result.Remaining < 0 || result.Remaining > result.Expected {
		return fmt.Errorf("backfill audit count 无效")
	}
	failures := make([]backfillFailureMetadata, 0, len(result.Failures))
	for _, failure := range result.Failures {
		message := ""
		if failure.Err != nil {
			message = failure.Err.Error()
		}
		failures = append(failures, backfillFailureMetadata{
			Symbol: failure.Symbol, Start: failure.Start.UTC(), End: failure.End.UTC(), Error: message,
		})
	}
	gaps := make([]backfillGapMetadata, 0, len(result.PlannedGaps))
	for _, planned := range result.PlannedGaps {
		remaining := overlappingGapCount(planned, result.RemainingGaps)
		status := "RECOVERED"
		if remaining == planned.Count {
			status = "MISSING"
		} else if remaining > 0 {
			status = "PARTIAL"
		}
		gaps = append(gaps, backfillGapMetadata{
			Symbol: planned.Symbol, Start: planned.Start.UTC(), End: planned.End.UTC(),
			ExpectedCount: planned.Count, RemainingCount: remaining, Status: status,
			LastError: lastGapError(planned, result.Failures),
		})
	}
	metadata, err := json.Marshal(map[string]any{
		"symbols":          result.Symbols,
		"concurrency":      result.Concurrency,
		"present_before":   result.PresentBefore,
		"written":          result.Written,
		"archive_days":     result.ArchiveDays,
		"rest_requests":    result.RESTRequests,
		"gaps":             gaps,
		"remaining_ranges": result.RemainingGaps,
		"failures":         failures,
	})
	if err != nil {
		return fmt.Errorf("编码 backfill audit metadata: %w", err)
	}
	status := "SUCCEEDED"
	var errorMessage *string
	if result.Remaining > 0 || len(result.Failures) > 0 {
		status = "DEGRADED"
		message := fmt.Sprintf("remaining=%d failures=%d", result.Remaining, len(result.Failures))
		errorMessage = &message
	}
	idempotencyKey := fmt.Sprintf(
		"backfill:15m:%s:%s:%s",
		result.WindowStart.UTC().Format(time.RFC3339),
		result.WindowEnd.UTC().Format(time.RFC3339),
		startedAt.UTC().Format(time.RFC3339Nano),
	)
	_, err = r.pool.Exec(ctx, `
		INSERT INTO collection_runs (
			idempotency_key, job_type, window_start, window_end,
			expected_count, actual_count, missing_count, status,
			error_message, started_at, completed_at, metadata
		) VALUES ($1, 'KLINE_15M_BACKFILL', $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (idempotency_key) DO UPDATE SET
			expected_count = EXCLUDED.expected_count,
			actual_count = EXCLUDED.actual_count,
			missing_count = EXCLUDED.missing_count,
			status = EXCLUDED.status,
			error_message = EXCLUDED.error_message,
			started_at = EXCLUDED.started_at,
			completed_at = EXCLUDED.completed_at,
			metadata = EXCLUDED.metadata`,
		idempotencyKey,
		result.WindowStart.UTC(), result.WindowEnd.UTC(),
		result.Expected, result.Expected-result.Remaining, result.Remaining,
		status, errorMessage, startedAt.UTC(), completedAt.UTC(), metadata,
	)
	if err != nil {
		return fmt.Errorf("记录 backfill audit: %w", err)
	}
	return nil
}

func overlappingGapCount(target backfill.Gap, remaining []backfill.Gap) int {
	total := 0
	for _, gap := range remaining {
		if gap.Symbol != target.Symbol {
			continue
		}
		start := target.Start
		if gap.Start.After(start) {
			start = gap.Start
		}
		end := target.End
		if gap.End.Before(end) {
			end = gap.End
		}
		if start.Before(end) {
			total += int(end.Sub(start) / (15 * time.Minute))
		}
	}
	return total
}

func lastGapError(target backfill.Gap, failures []backfill.Failure) string {
	last := ""
	for _, failure := range failures {
		if failure.Symbol != target.Symbol || !failure.Start.Before(target.End) || !target.Start.Before(failure.End) {
			continue
		}
		if failure.Err != nil {
			last = failure.Err.Error()
		}
	}
	return last
}
