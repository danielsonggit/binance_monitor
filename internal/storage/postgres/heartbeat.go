package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HeartbeatStore struct {
	pool *pgxpool.Pool
}

func NewHeartbeatStore(pool *pgxpool.Pool) *HeartbeatStore {
	return &HeartbeatStore{pool: pool}
}

func (s *HeartbeatStore) Record(ctx context.Context, component, status string, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("编码 heartbeat details: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO system_heartbeats (component, observed_at, status, detail_json)
		VALUES ($1, now(), $2, $3)
		ON CONFLICT (component) DO UPDATE SET
			observed_at = excluded.observed_at,
			status = excluded.status,
			detail_json = excluded.detail_json`,
		component,
		status,
		encoded,
	)
	if err != nil {
		return fmt.Errorf("写入 %s heartbeat: %w", component, err)
	}
	return nil
}
