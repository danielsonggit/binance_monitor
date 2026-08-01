package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"binance-monitor/internal/binance"
	"binance-monitor/internal/catalog"
	"binance-monitor/internal/config"
	"binance-monitor/internal/httpjson"
	"binance-monitor/internal/ranking"
	"binance-monitor/internal/report"
	"binance-monitor/internal/telegram"
)

type Job func(context.Context) error

func NewJob(
	settings config.Settings,
	assets *catalog.Catalog,
	dryRun bool,
	output io.Writer,
	logger *slog.Logger,
) (Job, error) {
	httpClient := httpjson.New(settings.Timeout, settings.MaxRetries)
	binanceClient := binance.New(settings.BinanceBaseURL, httpClient)

	var telegramClient *telegram.Client
	if !dryRun {
		if err := settings.ValidateTelegram(); err != nil {
			return nil, err
		}
		telegramClient = telegram.New(
			settings.BotToken,
			settings.ChatID,
			httpClient,
			settings.MessageThreadID,
		)
	}

	return func(ctx context.Context) error {
		contracts, tickers, err := binanceClient.FetchMarket(ctx, settings.QuoteAssets)
		if err != nil {
			return err
		}
		rankings := ranking.Build(contracts, tickers, settings.TopN)
		now := time.Now().In(settings.Location)
		timezoneLabel := settings.TimezoneName
		if timezoneLabel == "Asia/Shanghai" {
			timezoneLabel = "北京时间"
		}
		messages := report.TelegramMessages(
			rankings,
			now,
			assets,
			report.Options{
				TopN:          settings.TopN,
				QuoteLabel:    strings.Join(settings.QuoteAssets, "/"),
				TimezoneLabel: timezoneLabel,
			},
		)
		logger.Info(
			"市场读取完成",
			"contracts", len(contracts),
			"tickers", len(tickers),
			"messages", len(messages),
		)
		if dryRun {
			if _, err := fmt.Fprintln(output, report.Plain(messages)); err != nil {
				return fmt.Errorf("输出预览报告: %w", err)
			}
			return nil
		}
		ids, err := telegramClient.SendMessages(ctx, messages)
		if err != nil {
			return err
		}
		logger.Info("Telegram 推送完成", "message_ids", ids)
		return nil
	}, nil
}
