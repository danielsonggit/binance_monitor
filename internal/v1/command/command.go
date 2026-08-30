package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"binance-monitor/internal/app"
	"binance-monitor/internal/catalog"
	"binance-monitor/internal/config"
	"binance-monitor/internal/scheduler"
)

type Options struct {
	Once        bool
	Daemon      bool
	DryRun      bool
	CatalogPath string
	EnvFile     string
	Verbose     bool
}

func Run(ctx context.Context, options Options, stdout, stderr io.Writer) error {
	if options.Once && options.Daemon {
		return errors.New("--once 与 --daemon 不能同时使用")
	}
	if err := config.LoadDotEnv(options.EnvFile); err != nil {
		return err
	}
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("读取当前目录: %w", err)
	}
	settings, err := config.FromEnv(projectDir)
	if err != nil {
		return err
	}

	var assets *catalog.Catalog
	if options.CatalogPath == "" {
		assets, err = catalog.Default()
	} else {
		assets, err = catalog.FromFile(options.CatalogPath)
	}
	if err != nil {
		return err
	}

	level := slog.LevelInfo
	if options.Verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	job, err := app.NewJob(settings, assets, options.DryRun, stdout, logger)
	if err != nil {
		return err
	}
	if options.Once || options.DryRun {
		return job(ctx)
	}
	return scheduler.Run(
		ctx,
		job,
		settings.Location,
		settings.ReportHours,
		settings.GraceMinutes,
		scheduler.NewStore(settings.StateFile),
		logger,
	)
}

func BindFlags(command FlagBinder, options *Options) {
	command.BoolVar(&options.Once, "once", false, "立即抓取并推送一次")
	command.BoolVar(&options.Daemon, "daemon", false, "按配置时点持续运行（默认）")
	command.BoolVar(&options.DryRun, "dry-run", false, "抓取并打印报告，不推送 Telegram")
	command.StringVar(&options.CatalogPath, "catalog", "", "覆盖内置资产简介的 JSON 文件")
	command.StringVar(&options.EnvFile, "env-file", ".env", "V1 环境变量文件")
	command.BoolVar(&options.Verbose, "verbose", false, "输出调试日志")
}

// FlagBinder is the small part of pflag.FlagSet required by the V1 command.
// Keeping it as an interface makes the application runner independent of Cobra.
type FlagBinder interface {
	BoolVar(*bool, string, bool, string)
	StringVar(*string, string, string, string)
}
