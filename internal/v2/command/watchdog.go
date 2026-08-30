package command

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	legacyconfig "binance-monitor/internal/config"
	"binance-monitor/internal/httpjson"
	"binance-monitor/internal/telegram"
	"binance-monitor/internal/v2/watchdog"

	"github.com/spf13/cobra"
)

func newWatchdogCommand(stdout, stderr io.Writer) *cobra.Command {
	var opts options
	var once bool
	var testNotification bool
	command := &cobra.Command{
		Use:   "watchdog",
		Short: "独立监控 V2 健康并发送故障/恢复通知",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			envFile, err := filepath.Abs(opts.envFile)
			if err != nil {
				return fmt.Errorf("解析 watchdog env file: %w", err)
			}
			if err := legacyconfig.LoadDotEnv(envFile); err != nil {
				return err
			}
			settings, err := watchdog.SettingsFromEnv(filepath.Dir(envFile))
			if err != nil {
				return err
			}
			level := slog.LevelInfo
			if opts.verbose {
				level = slog.LevelDebug
			}
			logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
			store, err := watchdog.NewFileStore(settings.StateFile)
			if err != nil {
				return err
			}
			chatIDs := settings.TelegramChatIDs
			var notifier watchdog.Notifier
			if settings.DryRun {
				if len(chatIDs) == 0 {
					chatIDs = []string{"dry-run"}
				}
				notifier = loggingWatchdogNotifier{logger: logger}
			} else {
				httpClient, err := httpjson.NewWithProxy(settings.RequestTimeout, 1, settings.ProxyURL)
				if err != nil {
					return err
				}
				notifier = telegramWatchdogNotifier{
					client: telegram.New(settings.TelegramBotToken, "", httpClient, settings.TelegramThreadID),
				}
			}
			service, err := watchdog.NewService(
				watchdog.NewHTTPProbe(settings), store, notifier, chatIDs,
				settings.FailureThreshold, settings.RecoveryThreshold,
				settings.PollEvery, settings.Location, logger,
			)
			if err != nil {
				return err
			}
			if testNotification {
				if settings.DryRun {
					return fmt.Errorf("--test-notification 要求 WATCHDOG_DRY_RUN=false")
				}
				message := "✅ <b>Binance Radar V2 监控已启用</b>\n\n这是一条 watchdog 通道测试消息。后续只有连续故障或稳定恢复时才会通知。"
				for _, chatID := range chatIDs {
					if err := notifier.Send(command.Context(), chatID, message); err != nil {
						return fmt.Errorf("发送 watchdog 测试通知到 %s: %w", chatID, err)
					}
				}
				_, err = fmt.Fprintf(stdout, "watchdog 测试通知已发送：recipients=%d\n", len(chatIDs))
				return err
			}
			if !once {
				logger.Info("V2 watchdog 已启动", "dry_run", settings.DryRun, "poll_interval", settings.PollEvery)
				return service.Run(command.Context())
			}
			result, state, err := service.RunOnce(command.Context())
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(stdout,
				"watchdog 检查：healthy=%t active_incident=%t failures=%d recoveries=%d reason=%q\n",
				result.Healthy, state.Active, state.FailureCount, state.RecoveryCount, result.Reason(),
			)
			return err
		},
	}
	bindFlags(command, &opts)
	command.Flags().BoolVar(&once, "once", false, "只执行一次检查后退出")
	command.Flags().BoolVar(&testNotification, "test-notification", false, "发送一条明确的通道测试消息后退出")
	return command
}

type telegramWatchdogNotifier struct {
	client *telegram.Client
}

func (n telegramWatchdogNotifier) Send(ctx context.Context, chatID, message string) error {
	_, err := n.client.SendTo(ctx, chatID, message)
	return err
}

type loggingWatchdogNotifier struct {
	logger *slog.Logger
}

func (n loggingWatchdogNotifier) Send(_ context.Context, chatID, message string) error {
	n.logger.Warn("watchdog dry-run 通知", "chat_id", chatID, "message", message)
	return nil
}
