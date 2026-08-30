package watchdog

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIBaseURL        = "http://127.0.0.1:18080"
	defaultPollSeconds       = 60
	defaultFailureThreshold  = 3
	defaultRecoveryThreshold = 2
	defaultRequestTimeout    = 5
	defaultHeartbeatMaxAge   = 90
	defaultMarketMaxAge      = 120
	defaultDataMaxAge        = 600
)

type Settings struct {
	APIBaseURL        string
	PollEvery         time.Duration
	FailureThreshold  int
	RecoveryThreshold int
	RequestTimeout    time.Duration
	HeartbeatMaxAge   time.Duration
	MarketMaxAge      time.Duration
	DataMaxAge        time.Duration
	StateFile         string
	DryRun            bool
	TelegramBotToken  string
	TelegramChatIDs   []string
	TelegramThreadID  *int64
	ProxyURL          string
	Location          *time.Location
}

func SettingsFromEnv(baseDir string) (Settings, error) {
	apiBaseURL := strings.TrimRight(strings.TrimSpace(envOr("WATCHDOG_API_BASE_URL", defaultAPIBaseURL)), "/")
	parsedAPI, err := url.Parse(apiBaseURL)
	if err != nil || (parsedAPI.Scheme != "http" && parsedAPI.Scheme != "https") || parsedAPI.Host == "" {
		return Settings{}, fmt.Errorf("WATCHDOG_API_BASE_URL 必须是有效的 http/https URL")
	}
	pollSeconds, err := positiveInt("WATCHDOG_POLL_SECONDS", defaultPollSeconds)
	if err != nil {
		return Settings{}, err
	}
	failureThreshold, err := positiveInt("WATCHDOG_FAILURE_THRESHOLD", defaultFailureThreshold)
	if err != nil {
		return Settings{}, err
	}
	recoveryThreshold, err := positiveInt("WATCHDOG_RECOVERY_THRESHOLD", defaultRecoveryThreshold)
	if err != nil {
		return Settings{}, err
	}
	requestTimeout, err := positiveInt("WATCHDOG_REQUEST_TIMEOUT_SECONDS", defaultRequestTimeout)
	if err != nil {
		return Settings{}, err
	}
	heartbeatMaxAge, err := positiveInt("WATCHDOG_HEARTBEAT_MAX_AGE_SECONDS", defaultHeartbeatMaxAge)
	if err != nil {
		return Settings{}, err
	}
	marketMaxAge, err := positiveInt("WATCHDOG_MARKET_MAX_AGE_SECONDS", defaultMarketMaxAge)
	if err != nil {
		return Settings{}, err
	}
	dataMaxAge, err := positiveInt("WATCHDOG_DATA_MAX_AGE_SECONDS", defaultDataMaxAge)
	if err != nil {
		return Settings{}, err
	}
	dryRun, err := strconv.ParseBool(envOr("WATCHDOG_DRY_RUN", "true"))
	if err != nil {
		return Settings{}, fmt.Errorf("WATCHDOG_DRY_RUN 必须是 true 或 false")
	}
	stateFile := strings.TrimSpace(envOr("WATCHDOG_STATE_FILE", "state/watchdog.json"))
	if stateFile == "" {
		return Settings{}, fmt.Errorf("WATCHDOG_STATE_FILE 不能为空")
	}
	if !filepath.IsAbs(stateFile) {
		stateFile = filepath.Join(baseDir, stateFile)
	}
	proxyURL := strings.TrimSpace(os.Getenv("HTTP_PROXY_URL"))
	if proxyURL != "" {
		parsedProxy, parseErr := url.Parse(proxyURL)
		if parseErr != nil || (parsedProxy.Scheme != "http" && parsedProxy.Scheme != "https") || parsedProxy.Host == "" {
			return Settings{}, fmt.Errorf("HTTP_PROXY_URL 必须是有效的 http/https URL")
		}
	}
	timezone := envOr("APP_TIMEZONE", "Asia/Shanghai")
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Settings{}, fmt.Errorf("无效 APP_TIMEZONE %q: %w", timezone, err)
	}
	botToken := strings.TrimSpace(os.Getenv("WATCHDOG_TELEGRAM_BOT_TOKEN"))
	if botToken == "" {
		botToken = strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	}
	chatIDs := splitUnique(strings.TrimSpace(os.Getenv("WATCHDOG_TELEGRAM_CHAT_IDS")))
	if len(chatIDs) == 0 {
		chatIDs = splitUnique(strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_IDS")))
	}
	if len(chatIDs) == 0 {
		chatIDs = splitUnique(strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")))
	}
	threadID, err := optionalPositiveInt64(
		"WATCHDOG_TELEGRAM_MESSAGE_THREAD_ID",
		strings.TrimSpace(os.Getenv("WATCHDOG_TELEGRAM_MESSAGE_THREAD_ID")),
	)
	if err != nil {
		return Settings{}, err
	}
	if threadID == nil {
		threadID, err = optionalPositiveInt64(
			"TELEGRAM_MESSAGE_THREAD_ID",
			strings.TrimSpace(os.Getenv("TELEGRAM_MESSAGE_THREAD_ID")),
		)
		if err != nil {
			return Settings{}, err
		}
	}
	settings := Settings{
		APIBaseURL: apiBaseURL, PollEvery: time.Duration(pollSeconds) * time.Second,
		FailureThreshold: failureThreshold, RecoveryThreshold: recoveryThreshold,
		RequestTimeout:  time.Duration(requestTimeout) * time.Second,
		HeartbeatMaxAge: time.Duration(heartbeatMaxAge) * time.Second,
		MarketMaxAge:    time.Duration(marketMaxAge) * time.Second,
		DataMaxAge:      time.Duration(dataMaxAge) * time.Second,
		StateFile:       stateFile, DryRun: dryRun,
		TelegramBotToken: botToken,
		TelegramChatIDs:  chatIDs,
		TelegramThreadID: threadID,
		ProxyURL:         proxyURL, Location: location,
	}
	if !settings.DryRun && (settings.TelegramBotToken == "" || len(settings.TelegramChatIDs) == 0) {
		return Settings{}, fmt.Errorf("非 dry-run watchdog 需要 WATCHDOG_TELEGRAM_BOT_TOKEN 和 WATCHDOG_TELEGRAM_CHAT_IDS")
	}
	return settings, nil
}

func optionalPositiveInt64(key, raw string) (*int64, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return nil, fmt.Errorf("%s 必须是大于 0 的整数", key)
	}
	return &value, nil
}

func envOr(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func positiveInt(key string, fallback int) (int, error) {
	raw := envOr(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s 必须是大于 0 的整数", key)
	}
	return value, nil
}

func splitUnique(raw string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
