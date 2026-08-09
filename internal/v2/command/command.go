package command

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"binance-monitor/internal/backfill"
	"binance-monitor/internal/binance"
	"binance-monitor/internal/binancevision"
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
		newDatabaseCommand("backfill", "回补 V2 历史行情数据", stdout, stderr, runBackfill),
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

func runBackfill(
	ctx context.Context,
	settings v2config.Settings,
	resources *postgres.Resources,
	_ *slog.Logger,
	stdout io.Writer,
) error {
	httpClient, err := httpjson.NewWithProxy(settings.HTTPTimeout, settings.HTTPMaxRetries, settings.ProxyURL)
	if err != nil {
		return err
	}
	requestLimiter, err := ratelimit.NewWeightLimiter(settings.RequestWeightPerMinute, settings.RequestWeightBurst)
	if err != nil {
		return err
	}
	restClient, err := binance.NewWithWeightLimiter(settings.BinanceBaseURL, httpClient, requestLimiter)
	if err != nil {
		return err
	}
	archiveClient, err := binancevision.New(settings.ProxyURL)
	if err != nil {
		return err
	}
	service, err := backfill.NewService(postgres.NewKlineRepository(resources.Pool), archiveClient, restClient, backfill.SystemClock{})
	if err != nil {
		return err
	}
	startedAt := time.Now().UTC()
	result, err := service.Run(ctx, settings.BackfillLookback, settings.BackfillConcurrency)
	if err != nil {
		return err
	}
	if err := postgres.NewBackfillAuditRepository(resources.Pool).Record(
		ctx, result, startedAt, time.Now().UTC(),
	); err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(stdout,
		"backfill 完成：symbols=%d expected=%d present_before=%d written=%d archive_days=%d rest_requests=%d remaining=%d failures=%d\n",
		result.Symbols, result.Expected, result.PresentBefore, result.Written, result.ArchiveDays,
		result.RESTRequests, result.Remaining, len(result.Failures),
	)
	if writeErr != nil {
		return writeErr
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(stdout, "失败区间：symbol=%s start=%s end=%s error=%v\n",
			failure.Symbol, failure.Start.Format(time.RFC3339), failure.End.Format(time.RFC3339), failure.Err,
		); err != nil {
			return err
		}
	}
	for _, gap := range result.RemainingGaps {
		if _, err := fmt.Fprintf(stdout, "剩余缺口：symbol=%s start=%s end=%s count=%d\n",
			gap.Symbol, gap.Start.Format(time.RFC3339), gap.End.Format(time.RFC3339), gap.Count,
		); err != nil {
			return err
		}
	}
	if len(result.Failures) > 0 {
		return fmt.Errorf("backfill 有 %d 个失败区间", len(result.Failures))
	}
	return nil
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
