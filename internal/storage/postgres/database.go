package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, databaseURL string, maxConnections int32) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析 DATABASE_URL: %w", err)
	}
	poolConfig.MaxConns = maxConnections
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "binance-monitor-v2"

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("创建 PostgreSQL 连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL: %w", err)
	}
	return pool, nil
}

// Resources is the V2 PostgreSQL composition root. Repositories share one pool
// while commands own and close the resource lifetime.
type Resources struct {
	Pool *pgxpool.Pool
}

func OpenResources(ctx context.Context, databaseURL string, maxConnections int32) (*Resources, error) {
	pool, err := Open(ctx, databaseURL, maxConnections)
	if err != nil {
		return nil, err
	}
	return &Resources{Pool: pool}, nil
}

func (r *Resources) Close() {
	if r != nil && r.Pool != nil {
		r.Pool.Close()
	}
}
