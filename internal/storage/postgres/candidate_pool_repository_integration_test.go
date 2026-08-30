package postgres

import (
	"context"
	"testing"
	"time"

	"binance-monitor/internal/candidatepool"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/domain/signal"
	"github.com/jackc/pgx/v5/pgxpool"
)

type candidateIntegrationClock struct{ now time.Time }

func (c candidateIntegrationClock) Now() time.Time { return c.now }

func TestCandidatePoolRepositoryIntegration(t *testing.T) {
	ctx, pool := openMHR6IntegrationPool(t)
	if _, err := pool.Exec(ctx, `
		TRUNCATE candidate_evaluations, candidate_pool_members, candidate_rule_versions,
			collection_runs, return_feature_snapshots, market_snapshots_5m, klines_15m, instruments
		RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 8, 30, 6, 0, 0, 0, time.UTC)
	insertCandidateIntegrationWindow(t, ctx, pool, asOf, "4", "0")
	rules := signal.CandidateRulesV1()
	calculator, err := candidatepool.NewCalculator(rules)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewCandidatePoolRepository(pool)
	service, err := candidatepool.NewService(repository, calculator, candidateIntegrationClock{now: asOf.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.RunAt(ctx, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if first.Evaluated != 2 || first.Active != 1 || first.Entered != 1 || first.AlreadyApplied {
		t.Fatalf("first=%#v", first)
	}
	replayed, err := service.RunAt(ctx, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if !replayed.AlreadyApplied || replayed.Entered != 1 || replayed.Active != 1 {
		t.Fatalf("replayed=%#v", replayed)
	}

	nextAsOf := asOf.Add(5 * time.Minute)
	insertCandidateIntegrationWindow(t, ctx, pool, nextAsOf, "0", "0")
	service, _ = candidatepool.NewService(repository, calculator, candidateIntegrationClock{now: nextAsOf.Add(time.Second)})
	next, err := service.RunAt(ctx, nextAsOf)
	if err != nil {
		t.Fatal(err)
	}
	if next.Held != 1 || next.Active != 1 || next.Entered != 0 {
		t.Fatalf("next=%#v", next)
	}
	var evaluations, rulesCount, runs, misses int
	var outcome string
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM candidate_evaluations`).Scan(&evaluations); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM candidate_rule_versions`).Scan(&rulesCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM collection_runs WHERE job_type = $1`, candidatepool.JobType).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT e.outcome, m.consecutive_misses
		FROM candidate_evaluations e
		JOIN instruments i ON i.id = e.instrument_id
		JOIN candidate_pool_members m ON m.instrument_id = e.instrument_id
		WHERE i.symbol = 'AUSDT' AND e.as_of = $1`, nextAsOf).Scan(&outcome, &misses); err != nil {
		t.Fatal(err)
	}
	if evaluations != 4 || rulesCount != 1 || runs != 2 || outcome != string(signal.CandidateMissHeld) || misses != 1 {
		t.Fatalf("rows evaluations=%d rules=%d runs=%d outcome=%s misses=%d", evaluations, rulesCount, runs, outcome, misses)
	}
	queryResult, err := NewMarketQueryRepository(pool).LatestCandidates(ctx, market.SectorCrypto, signal.CandidateMemberActive)
	if err != nil {
		t.Fatal(err)
	}
	if queryResult.AsOf != nextAsOf || queryResult.Count != 1 || queryResult.Items[0].Symbol != "AUSDT" ||
		queryResult.Items[0].Outcome != signal.CandidateMissHeld || queryResult.Items[0].ConsecutiveMisses != 1 {
		t.Fatalf("candidate query=%#v", queryResult)
	}
}

func insertCandidateIntegrationWindow(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	asOf time.Time,
	returnA string,
	returnB string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO instruments (
			symbol, base_asset, quote_asset, sector, contract_type, status,
			valid_from, last_seen_at, exchange_status
		) VALUES
			('AUSDT','A','USDT','CRYPTO','PERPETUAL','TRADING',$1::timestamptz - interval '1 day',$1::timestamptz,'TRADING'),
			('BUSDT','B','USDT','CRYPTO','PERPETUAL','TRADING',$1::timestamptz - interval '1 day',$1::timestamptz,'TRADING')
		ON CONFLICT DO NOTHING`, asOf); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO return_feature_snapshots (
			instrument_id, as_of, feature_version, current_price, current_price_at,
			current_source, current_age_seconds, recent_quote_volume_1h, quote_volume_24h,
			return_15m, return_1h, return_4h, return_24h,
			is_valid_15m, is_valid_1h, is_valid_4h, is_valid_24h, quality_json, calculated_at
		)
		SELECT i.id,$1::timestamptz,'returns-v1',100,$1::timestamptz,'KLINE_15M',0,100000,2000000,
			CASE i.symbol WHEN 'AUSDT' THEN $2::numeric ELSE $3::numeric END,
			CASE i.symbol WHEN 'AUSDT' THEN $2::numeric ELSE $3::numeric END,
			0,0,true,true,true,true,'{}'::jsonb,$1::timestamptz + interval '1 second'
		FROM instruments i WHERE i.symbol IN ('AUSDT','BUSDT')
		ON CONFLICT DO NOTHING`, asOf, returnA, returnB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO collection_runs (
			idempotency_key,job_type,window_start,window_end,expected_count,actual_count,
			missing_count,status,completed_at,metadata
		) VALUES ($1,'MARKET_SNAPSHOT_5M',$2::timestamptz - interval '5 minutes',$2::timestamptz,2,2,0,'SUCCEEDED',$2::timestamptz + interval '1 second',
			'{"state_symbols":{"OPEN":["AUSDT","BUSDT"]}}'::jsonb)`,
		"candidate-test-snapshot:"+asOf.Format(time.RFC3339), asOf); err != nil {
		t.Fatal(err)
	}
}
