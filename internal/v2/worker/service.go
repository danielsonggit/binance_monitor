package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"binance-monitor/internal/universe"
)

const componentName = "v2-worker"

type HeartbeatRecorder interface {
	Record(context.Context, string, string, map[string]any) error
}

type UniverseSyncer interface {
	Sync(context.Context) (universe.Result, error)
}

type MarketRunner interface {
	Run(context.Context) error
	Health() (bool, time.Time, string)
}

type SnapshotRunner interface {
	Run(context.Context) error
	Health() (bool, time.Time, string)
}

type Service struct {
	heartbeats     HeartbeatRecorder
	universe       UniverseSyncer
	market         MarketRunner
	snapshots      SnapshotRunner
	heartbeatEvery time.Duration
	universeEvery  time.Duration
	logger         *slog.Logger
}

func New(
	heartbeats HeartbeatRecorder,
	universeSyncer UniverseSyncer,
	marketRunner MarketRunner,
	snapshotRunner SnapshotRunner,
	heartbeatEvery time.Duration,
	universeEvery time.Duration,
	logger *slog.Logger,
) *Service {
	return &Service{
		heartbeats:     heartbeats,
		universe:       universeSyncer,
		market:         marketRunner,
		snapshots:      snapshotRunner,
		heartbeatEvery: heartbeatEvery,
		universeEvery:  universeEvery,
		logger:         logger,
	}
}

func (s *Service) Run(ctx context.Context) error {
	runContext, cancelRunners := context.WithCancel(ctx)
	defer cancelRunners()
	if err := s.record(ctx, "STARTING", "", false, time.Time{}, "", false, time.Time{}, ""); err != nil {
		return err
	}
	type runnerError struct {
		name string
		err  error
	}
	runnerErrors := make(chan runnerError, 2)
	go func() {
		runnerErrors <- runnerError{name: "market", err: s.market.Run(runContext)}
	}()
	go func() {
		runnerErrors <- runnerError{name: "snapshot", err: s.snapshots.Run(runContext)}
	}()
	lastUniverseError := s.syncUniverse(ctx)
	if err := s.recordCurrentStatus(ctx, lastUniverseError); err != nil {
		return err
	}
	s.logger.Info(
		"V2 worker 已启动",
		"heartbeat_interval", s.heartbeatEvery,
		"universe_interval", s.universeEvery,
	)

	heartbeatTicker := time.NewTicker(s.heartbeatEvery)
	defer heartbeatTicker.Stop()
	universeTicker := time.NewTicker(s.universeEvery)
	defer universeTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := s.record(shutdownContext, "STOPPING", "", false, time.Time{}, "", false, time.Time{}, ""); err != nil {
				s.logger.Warn("记录 worker 停止状态失败", "error", err)
			}
			return nil
		case failure := <-runnerErrors:
			if ctx.Err() != nil {
				return nil
			}
			if failure.err == nil {
				return fmt.Errorf("%s runner 意外停止", failure.name)
			}
			return fmt.Errorf("%s runner: %w", failure.name, failure.err)
		case <-heartbeatTicker.C:
			if err := s.recordCurrentStatus(ctx, lastUniverseError); err != nil {
				return err
			}
		case <-universeTicker.C:
			lastUniverseError = s.syncUniverse(ctx)
			if err := s.recordCurrentStatus(ctx, lastUniverseError); err != nil {
				return err
			}
		}
	}
}

func (s *Service) syncUniverse(ctx context.Context) error {
	result, err := s.universe.Sync(ctx)
	if err != nil {
		s.logger.Error("同步 Binance 合约目录失败", "error", err)
		return err
	}
	s.logger.Info(
		"Binance 合约目录同步完成",
		"observed", result.Observed,
		"active_before", result.ActiveBefore,
		"inserted", result.Inserted,
		"updated", result.Updated,
		"missing_pending", result.MissingPending,
		"closed", result.Closed,
		"already_applied", result.AlreadyApplied,
	)
	return nil
}

func (s *Service) recordCurrentStatus(ctx context.Context, universeError error) error {
	marketConnected, lastMarketEvent, marketError := s.market.Health()
	snapshotHealthy, lastSnapshot, snapshotError := s.snapshots.Health()
	if universeError != nil {
		return s.record(ctx, "DEGRADED", universeError.Error(), marketConnected, lastMarketEvent, marketError, snapshotHealthy, lastSnapshot, snapshotError)
	}
	if !marketConnected || !snapshotHealthy {
		return s.record(ctx, "DEGRADED", "", marketConnected, lastMarketEvent, marketError, snapshotHealthy, lastSnapshot, snapshotError)
	}
	return s.record(ctx, "HEALTHY", "", marketConnected, lastMarketEvent, marketError, snapshotHealthy, lastSnapshot, snapshotError)
}

func (s *Service) record(
	ctx context.Context,
	status string,
	universeError string,
	marketConnected bool,
	lastMarketEvent time.Time,
	marketError string,
	snapshotHealthy bool,
	lastSnapshot time.Time,
	snapshotError string,
) error {
	return s.heartbeats.Record(ctx, componentName, status, map[string]any{
		"phase":             "phase1-market-snapshots",
		"universe_error":    universeError,
		"market_connected":  marketConnected,
		"last_market_event": lastMarketEvent,
		"market_error":      marketError,
		"snapshot_healthy":  snapshotHealthy,
		"last_snapshot":     lastSnapshot,
		"snapshot_error":    snapshotError,
	})
}
