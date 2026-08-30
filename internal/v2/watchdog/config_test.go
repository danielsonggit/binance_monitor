package watchdog

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSettingsFromEnv(t *testing.T) {
	baseDir := t.TempDir()
	t.Setenv("WATCHDOG_API_BASE_URL", "http://127.0.0.1:18080/")
	t.Setenv("WATCHDOG_POLL_SECONDS", "30")
	t.Setenv("WATCHDOG_FAILURE_THRESHOLD", "4")
	t.Setenv("WATCHDOG_RECOVERY_THRESHOLD", "3")
	t.Setenv("WATCHDOG_REQUEST_TIMEOUT_SECONDS", "7")
	t.Setenv("WATCHDOG_HEARTBEAT_MAX_AGE_SECONDS", "91")
	t.Setenv("WATCHDOG_MARKET_MAX_AGE_SECONDS", "121")
	t.Setenv("WATCHDOG_DATA_MAX_AGE_SECONDS", "601")
	t.Setenv("WATCHDOG_STATE_FILE", "state/test.json")
	t.Setenv("WATCHDOG_DRY_RUN", "false")
	t.Setenv("WATCHDOG_TELEGRAM_BOT_TOKEN", "token")
	t.Setenv("WATCHDOG_TELEGRAM_CHAT_IDS", "-1,-2,-1")
	t.Setenv("WATCHDOG_TELEGRAM_MESSAGE_THREAD_ID", "42")
	t.Setenv("HTTP_PROXY_URL", "http://127.0.0.1:7890")
	t.Setenv("APP_TIMEZONE", "Asia/Shanghai")

	settings, err := SettingsFromEnv(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if settings.APIBaseURL != "http://127.0.0.1:18080" || settings.PollEvery != 30*time.Second ||
		settings.FailureThreshold != 4 || settings.RecoveryThreshold != 3 || settings.RequestTimeout != 7*time.Second ||
		settings.HeartbeatMaxAge != 91*time.Second || settings.MarketMaxAge != 121*time.Second ||
		settings.DataMaxAge != 601*time.Second || settings.DryRun || settings.ProxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("settings = %#v", settings)
	}
	if settings.StateFile != filepath.Join(baseDir, "state/test.json") || settings.TelegramThreadID == nil || *settings.TelegramThreadID != 42 ||
		!reflect.DeepEqual(settings.TelegramChatIDs, []string{"-1", "-2"}) {
		t.Fatalf("state/chat ids = %s/%v", settings.StateFile, settings.TelegramChatIDs)
	}
}

func TestSettingsFromEnvFallsBackToV1TelegramVariables(t *testing.T) {
	clearWatchdogEnv(t)
	t.Setenv("WATCHDOG_DRY_RUN", "false")
	t.Setenv("TELEGRAM_BOT_TOKEN", "v1-token")
	t.Setenv("TELEGRAM_CHAT_ID", "-100")
	t.Setenv("TELEGRAM_MESSAGE_THREAD_ID", "88")
	settings, err := SettingsFromEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if settings.TelegramBotToken != "v1-token" || !reflect.DeepEqual(settings.TelegramChatIDs, []string{"-100"}) ||
		settings.TelegramThreadID == nil || *settings.TelegramThreadID != 88 {
		t.Fatalf("telegram fallback = %#v", settings)
	}
}

func TestSettingsFromEnvDefaultsToDryRunWithoutTelegram(t *testing.T) {
	clearWatchdogEnv(t)
	settings, err := SettingsFromEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !settings.DryRun || settings.FailureThreshold != 3 || settings.RecoveryThreshold != 2 ||
		settings.PollEvery != time.Minute {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestSettingsFromEnvRequiresTelegramWhenLive(t *testing.T) {
	clearWatchdogEnv(t)
	t.Setenv("WATCHDOG_DRY_RUN", "false")
	if _, err := SettingsFromEnv(t.TempDir()); err == nil {
		t.Fatal("expected Telegram configuration error")
	}
}

func TestSettingsFromEnvRejectsInvalidValues(t *testing.T) {
	for _, test := range []struct{ key, value string }{
		{"WATCHDOG_API_BASE_URL", "file:///tmp/api"},
		{"WATCHDOG_POLL_SECONDS", "0"},
		{"WATCHDOG_DRY_RUN", "sometimes"},
		{"HTTP_PROXY_URL", "socks5://127.0.0.1:7890"},
	} {
		t.Run(test.key, func(t *testing.T) {
			clearWatchdogEnv(t)
			t.Setenv(test.key, test.value)
			if _, err := SettingsFromEnv(t.TempDir()); err == nil {
				t.Fatalf("expected %s error", test.key)
			}
		})
	}
}

func clearWatchdogEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"WATCHDOG_API_BASE_URL", "WATCHDOG_POLL_SECONDS", "WATCHDOG_FAILURE_THRESHOLD",
		"WATCHDOG_RECOVERY_THRESHOLD", "WATCHDOG_REQUEST_TIMEOUT_SECONDS",
		"WATCHDOG_HEARTBEAT_MAX_AGE_SECONDS", "WATCHDOG_MARKET_MAX_AGE_SECONDS",
		"WATCHDOG_DATA_MAX_AGE_SECONDS", "WATCHDOG_STATE_FILE", "WATCHDOG_DRY_RUN",
		"WATCHDOG_TELEGRAM_BOT_TOKEN", "WATCHDOG_TELEGRAM_CHAT_IDS", "WATCHDOG_TELEGRAM_MESSAGE_THREAD_ID",
		"TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_IDS", "TELEGRAM_CHAT_ID", "TELEGRAM_MESSAGE_THREAD_ID",
		"HTTP_PROXY_URL", "APP_TIMEZONE",
	} {
		t.Setenv(key, "")
	}
	// Empty variables override defaults, so restore the booleans/URLs that the
	// parser intentionally obtains via envOr.
	t.Setenv("WATCHDOG_API_BASE_URL", defaultAPIBaseURL)
	t.Setenv("WATCHDOG_POLL_SECONDS", "60")
	t.Setenv("WATCHDOG_FAILURE_THRESHOLD", "3")
	t.Setenv("WATCHDOG_RECOVERY_THRESHOLD", "2")
	t.Setenv("WATCHDOG_REQUEST_TIMEOUT_SECONDS", "5")
	t.Setenv("WATCHDOG_HEARTBEAT_MAX_AGE_SECONDS", "90")
	t.Setenv("WATCHDOG_MARKET_MAX_AGE_SECONDS", "120")
	t.Setenv("WATCHDOG_DATA_MAX_AGE_SECONDS", "600")
	t.Setenv("WATCHDOG_STATE_FILE", "state/watchdog.json")
	t.Setenv("WATCHDOG_DRY_RUN", "true")
	t.Setenv("APP_TIMEZONE", "Asia/Shanghai")
}
