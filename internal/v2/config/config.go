package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultWebListenAddr    = "127.0.0.1:8080"
	defaultDatabaseMaxConns = 10
	defaultShutdownSeconds  = 15
	defaultHeartbeatSeconds = 15
	defaultHTTPTimeout      = 20
	defaultHTTPMaxRetries   = 3
	defaultWeightPerMinute  = 1800
	defaultWeightBurst      = 50
	binanceWeightLimit      = 2400
	maxKlineRequestWeight   = 10
	defaultUniverseMinutes  = 60
	defaultUniverseRatio    = 80
	defaultMissingConfirms  = 2
	defaultWSStaleSeconds   = 30
	defaultWSRotateMinutes  = 23*60 + 30
	defaultWSReconnectSecs  = 5
	defaultWindowMinutes    = 120
	defaultSnapshotMaxAge   = 90
)

// Settings contains only V2 infrastructure settings. V1 configuration remains
// in internal/config so the two runtimes can evolve independently.
type Settings struct {
	DatabaseURL            string
	DatabaseMaxConns       int32
	QuoteAssets            []string
	TimezoneName           string
	Location               *time.Location
	ProxyURL               string
	WebListenAddr          string
	ShutdownTimeout        time.Duration
	HeartbeatEvery         time.Duration
	BinanceBaseURL         string
	HTTPTimeout            time.Duration
	HTTPMaxRetries         int
	RequestWeightPerMinute int
	RequestWeightBurst     int
	UniverseEvery          time.Duration
	UniverseMinRatio       int
	MissingConfirms        int
	BinanceWSBaseURL       string
	WSStaleAfter           time.Duration
	WSRotateAfter          time.Duration
	WSReconnectWait        time.Duration
	MarketWindow           time.Duration
	SnapshotMaxAge         time.Duration
}

func FromEnv() (Settings, error) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return Settings{}, fmt.Errorf("DATABASE_URL 不能为空")
	}

	maxConns, err := positiveInt("DATABASE_MAX_CONNS", defaultDatabaseMaxConns)
	if err != nil {
		return Settings{}, err
	}
	if maxConns > 100 {
		return Settings{}, fmt.Errorf("DATABASE_MAX_CONNS 不能大于 100")
	}

	timezoneName := envOr("APP_TIMEZONE", "Asia/Shanghai")
	location, err := time.LoadLocation(timezoneName)
	if err != nil {
		return Settings{}, fmt.Errorf("无效 APP_TIMEZONE %q: %w", timezoneName, err)
	}

	quoteAssets := splitUniqueUpper(envOr("QUOTE_ASSETS", "USDT,USDC"))
	if len(quoteAssets) == 0 {
		return Settings{}, fmt.Errorf("QUOTE_ASSETS 不能为空")
	}

	proxyURL := strings.TrimSpace(os.Getenv("HTTP_PROXY_URL"))
	if err := validateProxyURL(proxyURL); err != nil {
		return Settings{}, err
	}

	webListenAddr := envOr("WEB_LISTEN_ADDR", defaultWebListenAddr)
	if _, _, err := net.SplitHostPort(webListenAddr); err != nil {
		return Settings{}, fmt.Errorf("无效 WEB_LISTEN_ADDR %q: %w", webListenAddr, err)
	}

	shutdownSeconds, err := positiveInt("SHUTDOWN_TIMEOUT_SECONDS", defaultShutdownSeconds)
	if err != nil {
		return Settings{}, err
	}
	heartbeatSeconds, err := positiveInt("WORKER_HEARTBEAT_SECONDS", defaultHeartbeatSeconds)
	if err != nil {
		return Settings{}, err
	}
	httpTimeoutSeconds, err := positiveInt("HTTP_TIMEOUT_SECONDS", defaultHTTPTimeout)
	if err != nil {
		return Settings{}, err
	}
	httpMaxRetries, err := positiveInt("HTTP_MAX_RETRIES", defaultHTTPMaxRetries)
	if err != nil {
		return Settings{}, err
	}
	requestWeightPerMinute, err := positiveInt("BINANCE_REQUEST_WEIGHT_PER_MINUTE", defaultWeightPerMinute)
	if err != nil {
		return Settings{}, err
	}
	if requestWeightPerMinute > binanceWeightLimit {
		return Settings{}, fmt.Errorf(
			"BINANCE_REQUEST_WEIGHT_PER_MINUTE 不能大于 Binance 限额 %d",
			binanceWeightLimit,
		)
	}
	requestWeightBurst, err := positiveInt("BINANCE_REQUEST_WEIGHT_BURST", defaultWeightBurst)
	if err != nil {
		return Settings{}, err
	}
	if requestWeightBurst < maxKlineRequestWeight {
		return Settings{}, fmt.Errorf(
			"BINANCE_REQUEST_WEIGHT_BURST 不能小于单次 K 线最大权重 %d",
			maxKlineRequestWeight,
		)
	}
	if requestWeightBurst > requestWeightPerMinute {
		return Settings{}, fmt.Errorf("BINANCE_REQUEST_WEIGHT_BURST 不能大于每分钟权重预算")
	}
	universeMinutes, err := positiveInt("UNIVERSE_SYNC_INTERVAL_MINUTES", defaultUniverseMinutes)
	if err != nil {
		return Settings{}, err
	}
	universeRatio, err := positiveInt("UNIVERSE_MINIMUM_RATIO_PERCENT", defaultUniverseRatio)
	if err != nil {
		return Settings{}, err
	}
	if universeRatio > 100 {
		return Settings{}, fmt.Errorf("UNIVERSE_MINIMUM_RATIO_PERCENT 不能大于 100")
	}
	missingConfirms, err := positiveInt("UNIVERSE_MISSING_CONFIRMATIONS", defaultMissingConfirms)
	if err != nil {
		return Settings{}, err
	}
	wsStaleSeconds, err := positiveInt("WS_STALE_AFTER_SECONDS", defaultWSStaleSeconds)
	if err != nil {
		return Settings{}, err
	}
	wsRotateMinutes, err := positiveInt("WS_ROTATE_AFTER_MINUTES", defaultWSRotateMinutes)
	if err != nil {
		return Settings{}, err
	}
	wsReconnectSeconds, err := positiveInt("WS_RECONNECT_WAIT_SECONDS", defaultWSReconnectSecs)
	if err != nil {
		return Settings{}, err
	}
	if time.Duration(wsRotateMinutes)*time.Minute <= time.Duration(wsStaleSeconds)*time.Second {
		return Settings{}, fmt.Errorf("WS_ROTATE_AFTER_MINUTES 必须大于 WS_STALE_AFTER_SECONDS")
	}
	windowMinutes, err := positiveInt("MARKET_WINDOW_RETENTION_MINUTES", defaultWindowMinutes)
	if err != nil {
		return Settings{}, err
	}
	if windowMinutes < 60 {
		return Settings{}, fmt.Errorf("MARKET_WINDOW_RETENTION_MINUTES 不能小于 60")
	}
	snapshotMaxAgeSeconds, err := positiveInt("SNAPSHOT_MAX_EVENT_AGE_SECONDS", defaultSnapshotMaxAge)
	if err != nil {
		return Settings{}, err
	}
	if time.Duration(snapshotMaxAgeSeconds)*time.Second >= 5*time.Minute {
		return Settings{}, fmt.Errorf("SNAPSHOT_MAX_EVENT_AGE_SECONDS 必须小于 300")
	}

	return Settings{
		DatabaseURL:            databaseURL,
		DatabaseMaxConns:       int32(maxConns),
		QuoteAssets:            quoteAssets,
		TimezoneName:           timezoneName,
		Location:               location,
		ProxyURL:               proxyURL,
		WebListenAddr:          webListenAddr,
		ShutdownTimeout:        time.Duration(shutdownSeconds) * time.Second,
		HeartbeatEvery:         time.Duration(heartbeatSeconds) * time.Second,
		BinanceBaseURL:         strings.TrimRight(envOr("BINANCE_FAPI_BASE_URL", "https://fapi.binance.com"), "/"),
		HTTPTimeout:            time.Duration(httpTimeoutSeconds) * time.Second,
		HTTPMaxRetries:         httpMaxRetries,
		RequestWeightPerMinute: requestWeightPerMinute,
		RequestWeightBurst:     requestWeightBurst,
		UniverseEvery:          time.Duration(universeMinutes) * time.Minute,
		UniverseMinRatio:       universeRatio,
		MissingConfirms:        missingConfirms,
		BinanceWSBaseURL:       strings.TrimRight(envOr("BINANCE_WS_BASE_URL", "wss://fstream.binance.com"), "/"),
		WSStaleAfter:           time.Duration(wsStaleSeconds) * time.Second,
		WSRotateAfter:          time.Duration(wsRotateMinutes) * time.Minute,
		WSReconnectWait:        time.Duration(wsReconnectSeconds) * time.Second,
		MarketWindow:           time.Duration(windowMinutes) * time.Minute,
		SnapshotMaxAge:         time.Duration(snapshotMaxAgeSeconds) * time.Second,
	}, nil
}

func validateProxyURL(raw string) error {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("无效 HTTP_PROXY_URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("HTTP_PROXY_URL 只支持 http 或 https scheme")
	}
	if parsed.Host == "" {
		return fmt.Errorf("HTTP_PROXY_URL 必须包含主机和端口")
	}
	return nil
}

func envOr(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return strings.TrimSpace(value)
	}
	return fallback
}

func positiveInt(key string, fallback int) (int, error) {
	raw := envOr(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s 必须是大于 0 的整数，当前值：%q", key, raw)
	}
	return value, nil
}

func splitUniqueUpper(raw string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		value := strings.ToUpper(strings.TrimSpace(part))
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
