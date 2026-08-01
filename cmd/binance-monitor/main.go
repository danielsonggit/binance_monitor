package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	_ "time/tzdata"

	"binance-monitor/internal/app"
	"binance-monitor/internal/catalog"
	"binance-monitor/internal/config"
	"binance-monitor/internal/scheduler"
)

type options struct {
	once    bool
	daemon  bool
	dryRun  bool
	catalog string
	envFile string
	verbose bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	opts, err := parseFlags(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if opts.once && opts.daemon {
		return errors.New("--once 与 --daemon 不能同时使用")
	}
	if err := config.LoadDotEnv(opts.envFile); err != nil {
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
	if opts.catalog == "" {
		assets, err = catalog.Default()
	} else {
		assets, err = catalog.FromFile(opts.catalog)
	}
	if err != nil {
		return err
	}

	level := slog.LevelInfo
	if opts.verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	job, err := app.NewJob(settings, assets, opts.dryRun, os.Stdout, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if opts.once || opts.dryRun {
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

func parseFlags(arguments []string) (options, error) {
	var result options
	flags := flag.NewFlagSet("binance-monitor", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.BoolVar(&result.once, "once", false, "立即抓取并推送一次")
	flags.BoolVar(&result.daemon, "daemon", false, "按配置时点持续运行（默认）")
	flags.BoolVar(&result.dryRun, "dry-run", false, "抓取并打印报告，不推送 Telegram")
	flags.StringVar(&result.catalog, "catalog", "", "覆盖内置资产简介的 JSON 文件")
	flags.StringVar(&result.envFile, "env-file", ".env", "环境变量文件")
	flags.BoolVar(&result.verbose, "verbose", false, "输出调试日志")
	flags.Usage = func() {
		output := flags.Output()
		fmt.Fprintln(output, "Binance USDⓈ-M TradFi/Crypto 涨幅榜 Telegram 报告")
		fmt.Fprintf(output, "\n用法：%s [参数]\n\n参数：\n", filepath.Base(os.Args[0]))
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("不支持的位置参数：%v", flags.Args())
	}
	return result, nil
}
