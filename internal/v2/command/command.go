package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"binance-monitor/internal/binance"
	"binance-monitor/internal/binancews"
	"binance-monitor/internal/collector"
	legacyconfig "binance-monitor/internal/config"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/httpjson"
	"binance-monitor/internal/marketdata"
	"binance-monitor/internal/ratelimit"
	"binance-monitor/internal/storage/postgres"
	"binance-monitor/internal/universe"
	"binance-monitor/internal/v2/api"
	v2config "binance-monitor/internal/v2/config"
	"binance-monitor/internal/v2/worker"

	"github.com/spf13/cobra"
)

type options struct {
	envFile string
	verbose bool
}

func NewCommands(stdout, stderr io.Writer) []*cobra.Command {
	return []*cobra.Command{
		newDatabaseCommand("migrate", "执行 V2 PostgreSQL schema migration", stdout, stderr, runMigrate),
		newDatabaseCommand("worker", "运行 V2 市场采集与后台任务", stdout, stderr, runWorker),
		newDatabaseCommand("api", "运行 V2 只读 API 和健康接口", stdout, stderr, runAPI),
		newBackfillCommand(stderr),
	}
}

type databaseRunner func(
	context.Context,
	v2config.Settings,
	*postgres.Resources,
	*slog.Logger,
	io.Writer,
) error

func newDatabaseCommand(
	name string,
	short string,
	stdout io.Writer,
	stderr io.Writer,
	runner databaseRunner,
) *cobra.Command {
	var opts options
	command := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, logger, err := loadSettings(opts, stderr)
			if err != nil {
				return err
			}
			resources, err := postgres.OpenResources(
				command.Context(),
				settings.DatabaseURL,
				settings.DatabaseMaxConns,
			)
			if err != nil {
				return err
			}
			defer resources.Close()
			return runner(command.Context(), settings, resources, logger, stdout)
		},
	}
	bindFlags(command, &opts)
	return command
}

func newBackfillCommand(stderr io.Writer) *cobra.Command {
	var opts options
	command := &cobra.Command{
		Use:   "backfill",
		Short: "回补 V2 历史行情数据",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if _, _, err := loadSettings(opts, stderr); err != nil {
				return err
			}
			return errors.New("backfill 命令骨架已注册，历史行情采集将在 Phase 1 下一批实现")
		},
	}
	bindFlags(command, &opts)
	return command
}

func bindFlags(command *cobra.Command, options *options) {
	command.Flags().StringVar(&options.envFile, "env-file", ".env.v2", "V2 环境变量文件")
	command.Flags().BoolVar(&options.verbose, "verbose", false, "输出调试日志")
}

func loadSettings(options options, stderr io.Writer) (v2config.Settings, *slog.Logger, error) {
	if err := legacyconfig.LoadDotEnv(options.envFile); err != nil {
		return v2config.Settings{}, nil, err
	}
	settings, err := v2config.FromEnv()
	if err != nil {
		return v2config.Settings{}, nil, err
	}
	level := slog.LevelInfo
	if options.verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	return settings, logger, nil
}

func runMigrate(
	ctx context.Context,
	_ v2config.Settings,
	resources *postgres.Resources,
	_ *slog.Logger,
	stdout io.Writer,
) error {
	result, err := postgres.ApplyMigrations(ctx, resources.Pool)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "migration 完成：applied=%d current_version=%d\n", result.Applied, result.CurrentVersion)
	return err
}

func runWorker(
	ctx context.Context,
	settings v2config.Settings,
	resources *postgres.Resources,
	logger *slog.Logger,
	_ io.Writer,
) error {
	httpClient, err := httpjson.NewWithProxy(
		settings.HTTPTimeout,
		settings.HTTPMaxRetries,
		settings.ProxyURL,
	)
	if err != nil {
		return err
	}
	requestLimiter, err := ratelimit.NewWeightLimiter(
		settings.RequestWeightPerMinute,
		settings.RequestWeightBurst,
	)
	if err != nil {
		return err
	}
	binanceClient, err := binance.NewWithWeightLimiter(
		settings.BinanceBaseURL,
		httpClient,
		requestLimiter,
	)
	if err != nil {
		return err
	}
	universeService := universe.New(
		binanceClient,
		postgres.NewUniverseRepository(resources.Pool),
		universe.SystemClock{},
		settings.QuoteAssets,
		settings.UniverseMinRatio,
		settings.MissingConfirms,
	)
	wsConnector, err := binancews.NewSDKConnector(
		settings.BinanceWSBaseURL,
		settings.ProxyURL,
		settings.WSReconnectWait,
	)
	if err != nil {
		return err
	}
	latestMarket := marketdata.NewLatestStore()
	minuteWindows := marketdata.NewWindowStore(settings.MarketWindow)
	snapshotCollector, err := collector.NewSnapshotCollector(
		latestMarket,
		minuteWindows,
		postgres.NewMarketSnapshotRepository(resources.Pool),
		settings.MarketWindow,
		settings.SnapshotMaxAge,
		market.SnapshotInterval,
		logger,
	)
	if err != nil {
		return err
	}
	marketSupervisor := binancews.NewSupervisor(
		wsConnector,
		latestMarket,
		settings.WSStaleAfter,
		settings.WSRotateAfter,
		settings.WSReconnectWait,
		logger,
	)
	service := worker.New(
		postgres.NewHeartbeatStore(resources.Pool),
		universeService,
		marketSupervisor,
		snapshotCollector,
		settings.HeartbeatEvery,
		settings.UniverseEvery,
		logger,
	)
	return service.Run(ctx)
}

func runAPI(
	ctx context.Context,
	settings v2config.Settings,
	resources *postgres.Resources,
	logger *slog.Logger,
	_ io.Writer,
) error {
	server := api.New(settings.WebListenAddr, settings.ShutdownTimeout, resources.Pool, logger)
	return server.Run(ctx)
}
