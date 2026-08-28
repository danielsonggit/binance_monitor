package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"binance-monitor/internal/backfill"
	"binance-monitor/internal/binance"
	"binance-monitor/internal/binancevision"
	"binance-monitor/internal/binancews"
	"binance-monitor/internal/catalog"
	"binance-monitor/internal/collector"
	legacyconfig "binance-monitor/internal/config"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/feature"
	"binance-monitor/internal/httpjson"
	"binance-monitor/internal/marketdata"
	"binance-monitor/internal/marketquery"
	"binance-monitor/internal/notification"
	"binance-monitor/internal/ranking"
	"binance-monitor/internal/ratelimit"
	"binance-monitor/internal/storage/postgres"
	"binance-monitor/internal/telegram"
	"binance-monitor/internal/universe"
	"binance-monitor/internal/v2/api"
	v2config "binance-monitor/internal/v2/config"
	v2pipeline "binance-monitor/internal/v2/pipeline"
	v2report "binance-monitor/internal/v2/report"
	v2reporter "binance-monitor/internal/v2/reporter"
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
		newDatabaseCommand("features", "回补行情并计算 V2 多周期收益率与排名", stdout, stderr, runFeatures),
		newRankingsCommand(stdout, stderr),
		newCandidateAnalysisCommand(stdout, stderr),
		newDatabaseCommand("report", "预览最新 V2 多周期 Telegram 报告，不发送", stdout, stderr, runReport),
		newDatabaseCommand("reporter", "运行 V2 Telegram 定时报表与可靠发送器", stdout, stderr, runReporter),
		newWatchdogCommand(stdout, stderr),
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

func newRankingsCommand(stdout, stderr io.Writer) *cobra.Command {
	var opts options
	var asOfRaw string
	command := &cobra.Command{
		Use:   "rankings",
		Short: "从已落库收益特征生成 V2 多周期排名",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, logger, err := loadSettings(opts, stderr)
			if err != nil {
				return err
			}
			asOf := time.Now().UTC().Truncate(market.SnapshotInterval)
			if asOfRaw != "" {
				asOf, err = time.Parse(time.RFC3339, asOfRaw)
				if err != nil {
					return fmt.Errorf("--as-of 必须是 RFC3339 时间: %w", err)
				}
				asOf = asOf.UTC()
				if !asOf.Equal(asOf.Truncate(market.SnapshotInterval)) {
					return fmt.Errorf("--as-of 必须按 %s 对齐", market.SnapshotInterval)
				}
			}
			resources, err := postgres.OpenResources(command.Context(), settings.DatabaseURL, settings.DatabaseMaxConns)
			if err != nil {
				return err
			}
			defer resources.Close()
			service, err := newRankingService(settings, resources)
			if err != nil {
				return err
			}
			result, err := service.RunAt(command.Context(), asOf)
			if err != nil {
				return err
			}
			logger.Info("手动多周期排名生成完成", "as_of", result.AsOf)
			_, err = fmt.Fprintf(stdout,
				"rankings 完成：as_of=%s groups=%d active_metrics=%d eligible=%d positive=%d items=%d written=%d\n",
				result.AsOf.Format(time.RFC3339), result.Groups, result.ActiveMetrics,
				result.Eligible, result.Positive, result.Items, result.Written,
			)
			return err
		},
	}
	bindFlags(command, &opts)
	command.Flags().StringVar(&asOfRaw, "as-of", "", "指定已落库的 UTC RFC3339 五分钟时点；默认当前时点")
	return command
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
	clients, err := newMarketClients(settings)
	if err != nil {
		return err
	}
	service, err := newBackfillService(resources, clients)
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

func runFeatures(
	ctx context.Context,
	settings v2config.Settings,
	resources *postgres.Resources,
	logger *slog.Logger,
	stdout io.Writer,
) error {
	clients, err := newMarketClients(settings)
	if err != nil {
		return err
	}
	pipeline, err := newReturnPipeline(settings, resources, clients)
	if err != nil {
		return err
	}
	asOf := time.Now().UTC().Truncate(market.SnapshotInterval)
	result, err := pipeline.RunAt(ctx, asOf)
	if err != nil {
		return err
	}
	logger.Info("手动多周期收益率与排名计算完成", "as_of", result.Features.AsOf)
	reasons, err := json.Marshal(result.Features.InvalidReasons)
	if err != nil {
		return fmt.Errorf("编码 feature invalid reasons: %w", err)
	}
	_, err = fmt.Fprintf(stdout,
		"features 完成：as_of=%s symbols=%d valid_metrics=%d invalid_metrics=%d feature_rows=%d ranking_groups=%d ranking_items=%d eligible=%d positive=%d reasons=%s\n",
		result.Features.AsOf.Format(time.RFC3339), result.Features.Symbols, result.Features.ValidMetrics,
		result.Features.InvalidMetrics, result.Features.Written, result.Rankings.Groups,
		result.Rankings.Items, result.Rankings.Eligible, result.Rankings.Positive, reasons,
	)
	return err
}

func runWorker(
	ctx context.Context,
	settings v2config.Settings,
	resources *postgres.Resources,
	logger *slog.Logger,
	_ io.Writer,
) error {
	clients, err := newMarketClients(settings)
	if err != nil {
		return err
	}
	universeService := universe.New(
		clients.rest,
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
	marketSupervisor := binancews.NewSupervisor(
		wsConnector,
		latestMarket,
		settings.WSStaleAfter,
		settings.WSRotateAfter,
		settings.WSReconnectWait,
		logger,
	)
	snapshotCollector, err := collector.NewSnapshotCollector(
		latestMarket,
		marketSupervisor,
		minuteWindows,
		postgres.NewMarketSnapshotRepository(resources.Pool),
		settings.MarketWindow,
		settings.SnapshotMaxAge,
		market.SnapshotInterval,
		settings.SnapshotMinimumRatio,
		logger,
	)
	if err != nil {
		return err
	}
	returnPipeline, err := newReturnPipeline(settings, resources, clients)
	if err != nil {
		return err
	}
	analysisRunner, err := v2pipeline.NewRunner(
		returnPipeline,
		market.SnapshotInterval,
		settings.FeatureCalculationDelay,
		settings.FeatureMinimumCoverage,
		logger,
	)
	if err != nil {
		return err
	}
	service := worker.New(
		postgres.NewHeartbeatStore(resources.Pool),
		universeService,
		marketSupervisor,
		snapshotCollector,
		analysisRunner,
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
	reader, err := newMarketQueryService(resources)
	if err != nil {
		return err
	}
	server := api.New(settings.WebListenAddr, settings.ShutdownTimeout, resources.Pool, reader, logger)
	return server.Run(ctx)
}

func runReport(
	ctx context.Context,
	settings v2config.Settings,
	resources *postgres.Resources,
	_ *slog.Logger,
	stdout io.Writer,
) error {
	reports, err := newReportService(settings, resources)
	if err != nil {
		return err
	}
	snapshot, messages, err := reports.Messages(ctx)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "report 预览：as_of=%s messages=%d\n\n%s\n",
		snapshot.AsOf.Format(time.RFC3339), len(messages), v2report.Plain(messages)); err != nil {
		return err
	}
	return nil
}

func runReporter(
	ctx context.Context,
	settings v2config.Settings,
	resources *postgres.Resources,
	logger *slog.Logger,
	_ io.Writer,
) error {
	if settings.TelegramBotToken == "" || len(settings.TelegramChatIDs) == 0 {
		return fmt.Errorf("reporter 需要 TELEGRAM_BOT_TOKEN 和 TELEGRAM_CHAT_IDS")
	}
	reports, err := newReportService(settings, resources)
	if err != nil {
		return err
	}
	// Telegram transport must make exactly one HTTP attempt. The durable outbox
	// owns retries so a timeout cannot be retried invisibly and duplicate a send.
	httpClient, err := httpjson.NewWithProxy(settings.HTTPTimeout, 1, settings.ProxyURL)
	if err != nil {
		return err
	}
	notifications := postgres.NewNotificationRepository(resources.Pool)
	enqueuer, err := notification.NewEnqueuer(notifications, settings.TelegramChatIDs)
	if err != nil {
		return err
	}
	dispatcher, err := notification.NewDispatcher(
		notifications,
		telegram.New(settings.TelegramBotToken, "", httpClient, nil),
		settings.TelegramRetryBase,
		2*time.Minute,
	)
	if err != nil {
		return err
	}
	service, err := v2reporter.New(
		reports, enqueuer, dispatcher, v2reporter.SystemClock{},
		settings.Location, settings.ReportHours, settings.ReportGrace,
		settings.ReporterPollEvery, settings.TelegramMaxAttempts, logger,
	)
	if err != nil {
		return err
	}
	return service.Run(ctx)
}

type marketClients struct {
	rest    *binance.Client
	archive *binancevision.Client
}

func newMarketClients(settings v2config.Settings) (marketClients, error) {
	httpClient, err := httpjson.NewWithProxy(settings.HTTPTimeout, settings.HTTPMaxRetries, settings.ProxyURL)
	if err != nil {
		return marketClients{}, err
	}
	requestLimiter, err := ratelimit.NewWeightLimiter(settings.RequestWeightPerMinute, settings.RequestWeightBurst)
	if err != nil {
		return marketClients{}, err
	}
	restClient, err := binance.NewWithWeightLimiter(settings.BinanceBaseURL, httpClient, requestLimiter)
	if err != nil {
		return marketClients{}, err
	}
	archiveClient, err := binancevision.New(settings.ProxyURL)
	if err != nil {
		return marketClients{}, err
	}
	return marketClients{rest: restClient, archive: archiveClient}, nil
}

func newBackfillService(resources *postgres.Resources, clients marketClients) (*backfill.Service, error) {
	return backfill.NewService(
		postgres.NewKlineRepository(resources.Pool),
		clients.archive,
		clients.rest,
		backfill.SystemClock{},
	)
}

func newReturnPipeline(
	settings v2config.Settings,
	resources *postgres.Resources,
	clients marketClients,
) (*v2pipeline.ReturnPipeline, error) {
	history, err := newBackfillService(resources, clients)
	if err != nil {
		return nil, err
	}
	calculator, err := feature.NewCalculator(feature.Policy{
		CurrentMaxAge:     settings.FeatureCurrentMaxAge,
		BaselineMaxOffset: settings.FeatureBaselineMaxOffset,
		MinimumQuality:    settings.FeatureMinimumQuality,
		LiquidityLookback: time.Hour,
		FeatureVersion:    market.ReturnFeatureVersion1,
	})
	if err != nil {
		return nil, err
	}
	features, err := feature.NewService(
		postgres.NewReturnFeatureRepository(resources.Pool),
		calculator,
		feature.SystemClock{},
		settings.BackfillLookback,
	)
	if err != nil {
		return nil, err
	}
	rankings, err := newRankingService(settings, resources)
	if err != nil {
		return nil, err
	}
	return v2pipeline.NewReturnPipeline(
		history,
		postgres.NewBackfillAuditRepository(resources.Pool),
		features,
		rankings,
		settings.BackfillLookback,
		settings.BackfillConcurrency,
	)
}

func newRankingService(settings v2config.Settings, resources *postgres.Resources) (*ranking.Service, error) {
	calculator, err := ranking.NewCalculator(ranking.Policy{
		RankingVersion: market.RankingVersion1,
		FeatureVersion: market.ReturnFeatureVersion1,
		TopN:           settings.RankingTopN,
	})
	if err != nil {
		return nil, err
	}
	return ranking.NewService(
		postgres.NewRankingRepository(resources.Pool),
		calculator,
		ranking.SystemClock{},
	)
}

func newMarketQueryService(resources *postgres.Resources) (*marketquery.Service, error) {
	return marketquery.NewService(postgres.NewMarketQueryRepository(resources.Pool))
}

func newReportService(settings v2config.Settings, resources *postgres.Resources) (*v2report.Service, error) {
	reader, err := newMarketQueryService(resources)
	if err != nil {
		return nil, err
	}
	assets, err := catalog.Default()
	if err != nil {
		return nil, err
	}
	return v2report.NewService(reader, assets, settings.Location, settings.RankingTopN)
}
