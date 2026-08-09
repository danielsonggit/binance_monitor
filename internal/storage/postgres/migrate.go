package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const migrationLockID int64 = 7_354_662_021

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	Version  int64
	Name     string
	SQL      string
	Checksum string
}

type MigrationResult struct {
	Applied        int
	CurrentVersion int64
}

func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool) (MigrationResult, error) {
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		return MigrationResult{}, err
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		return MigrationResult{}, fmt.Errorf("获取 migration 数据库连接: %w", err)
	}
	defer connection.Release()

	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return MigrationResult{}, fmt.Errorf("获取 migration 锁: %w", err)
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = connection.Exec(unlockContext, "SELECT pg_advisory_unlock($1)", migrationLockID)
	}()

	if _, err := connection.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			checksum text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return MigrationResult{}, fmt.Errorf("创建 schema_migrations: %w", err)
	}

	applied, err := readAppliedMigrations(ctx, connection)
	if err != nil {
		return MigrationResult{}, err
	}

	result := MigrationResult{}
	for _, item := range migrations {
		if checksum, exists := applied[item.Version]; exists {
			if checksum != item.Checksum {
				return MigrationResult{}, fmt.Errorf("migration %d checksum 已变化，禁止修改已执行 migration", item.Version)
			}
			result.CurrentVersion = item.Version
			continue
		}

		transaction, err := connection.Begin(ctx)
		if err != nil {
			return MigrationResult{}, fmt.Errorf("开始 migration %d: %w", item.Version, err)
		}
		if _, err := transaction.Exec(ctx, item.SQL); err != nil {
			_ = transaction.Rollback(ctx)
			return MigrationResult{}, fmt.Errorf("执行 migration %d %s: %w", item.Version, item.Name, err)
		}
		if _, err := transaction.Exec(
			ctx,
			"INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
			item.Version,
			item.Name,
			item.Checksum,
		); err != nil {
			_ = transaction.Rollback(ctx)
			return MigrationResult{}, fmt.Errorf("记录 migration %d: %w", item.Version, err)
		}
		if err := transaction.Commit(ctx); err != nil {
			return MigrationResult{}, fmt.Errorf("提交 migration %d: %w", item.Version, err)
		}
		result.Applied++
		result.CurrentVersion = item.Version
	}
	return result, nil
}

func readAppliedMigrations(ctx context.Context, connection *pgxpool.Conn) (map[int64]string, error) {
	rows, err := connection.Query(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("读取 schema_migrations: %w", err)
	}
	defer rows.Close()

	result := make(map[int64]string)
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("解析 schema_migrations: %w", err)
		}
		result[version] = checksum
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 schema_migrations: %w", err)
	}
	return result, nil
}

func loadMigrations(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, "migrations")
	if err != nil {
		return nil, fmt.Errorf("读取 embedded migrations: %w", err)
	}

	result := make([]migration, 0, len(entries))
	versions := make(map[int64]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(entry.Name(), ".sql"), "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("migration 文件名必须是 <version>_<name>.sql: %s", entry.Name())
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration 版本无效: %s", entry.Name())
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("migration 版本 %d 重复: %s 和 %s", version, previous, entry.Name())
		}
		contents, err := fs.ReadFile(source, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("读取 migration %s: %w", entry.Name(), err)
		}
		digest := sha256.Sum256(contents)
		result = append(result, migration{
			Version:  version,
			Name:     parts[1],
			SQL:      string(contents),
			Checksum: hex.EncodeToString(digest[:]),
		})
		versions[version] = entry.Name()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	if len(result) == 0 {
		return nil, fmt.Errorf("没有找到 embedded migration")
	}
	return result, nil
}
