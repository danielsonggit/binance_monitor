package config

import (
	"reflect"
	"testing"
	"time"
)

func TestFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://radar:secret@localhost/radar")
	t.Setenv("DATABASE_MAX_CONNS", "12")
	t.Setenv("QUOTE_ASSETS", "usdt,USDC,usdt")
	t.Setenv("APP_TIMEZONE", "Asia/Shanghai")
	t.Setenv("HTTP_PROXY_URL", "http://127.0.0.1:7890")
	t.Setenv("WEB_LISTEN_ADDR", "127.0.0.1:18080")
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "20")
	t.Setenv("WORKER_HEARTBEAT_SECONDS", "5")
	t.Setenv("HTTP_TIMEOUT_SECONDS", "9")
	t.Setenv("HTTP_MAX_RETRIES", "4")
	t.Setenv("BINANCE_REQUEST_WEIGHT_PER_MINUTE", "1600")
	t.Setenv("BINANCE_REQUEST_WEIGHT_BURST", "40")
	t.Setenv("UNIVERSE_SYNC_INTERVAL_MINUTES", "30")
	t.Setenv("UNIVERSE_MINIMUM_RATIO_PERCENT", "85")
	t.Setenv("UNIVERSE_MISSING_CONFIRMATIONS", "3")
	t.Setenv("BINANCE_WS_BASE_URL", "wss://fstream.binance.com")
	t.Setenv("WS_STALE_AFTER_SECONDS", "40")
	t.Setenv("WS_ROTATE_AFTER_MINUTES", "1200")
	t.Setenv("WS_RECONNECT_WAIT_SECONDS", "6")
	t.Setenv("MARKET_WINDOW_RETENTION_MINUTES", "420")
	t.Setenv("SNAPSHOT_MAX_EVENT_AGE_SECONDS", "75")
	t.Setenv("BACKFILL_LOOKBACK_HOURS", "36")
	t.Setenv("BACKFILL_CONCURRENCY", "12")
	t.Setenv("FEATURE_CURRENT_MAX_AGE_SECONDS", "240")
	t.Setenv("FEATURE_BASELINE_MAX_OFFSET_SECONDS", "180")
	t.Setenv("FEATURE_MINIMUM_QUALITY_SCORE", "80")
	t.Setenv("FEATURE_CALCULATION_DELAY_SECONDS", "7")

	settings, err := FromEnv()
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if settings.DatabaseMaxConns != 12 {
		t.Errorf("DatabaseMaxConns = %d", settings.DatabaseMaxConns)
	}
	if !reflect.DeepEqual(settings.QuoteAssets, []string{"USDT", "USDC"}) {
		t.Errorf("QuoteAssets = %v", settings.QuoteAssets)
	}
	if settings.ShutdownTimeout != 20*time.Second || settings.HeartbeatEvery != 5*time.Second {
		t.Errorf("timeouts = %s, %s", settings.ShutdownTimeout, settings.HeartbeatEvery)
	}
	if settings.HTTPTimeout != 9*time.Second || settings.HTTPMaxRetries != 4 {
		t.Errorf("HTTP settings = %s/%d", settings.HTTPTimeout, settings.HTTPMaxRetries)
	}
	if settings.RequestWeightPerMinute != 1600 || settings.RequestWeightBurst != 40 {
		t.Errorf("request weight settings = %d/%d", settings.RequestWeightPerMinute, settings.RequestWeightBurst)
	}
	if settings.UniverseEvery != 30*time.Minute || settings.UniverseMinRatio != 85 || settings.MissingConfirms != 3 {
		t.Errorf("universe settings = %s/%d/%d", settings.UniverseEvery, settings.UniverseMinRatio, settings.MissingConfirms)
	}
	if settings.WSStaleAfter != 40*time.Second || settings.WSRotateAfter != 20*time.Hour || settings.WSReconnectWait != 6*time.Second {
		t.Errorf("websocket settings = %s/%s/%s", settings.WSStaleAfter, settings.WSRotateAfter, settings.WSReconnectWait)
	}
	if settings.MarketWindow != 7*time.Hour || settings.SnapshotMaxAge != 75*time.Second {
		t.Errorf("snapshot settings = %s/%s", settings.MarketWindow, settings.SnapshotMaxAge)
	}
	if settings.BackfillLookback != 36*time.Hour || settings.BackfillConcurrency != 12 {
		t.Errorf("backfill settings = %s/%d", settings.BackfillLookback, settings.BackfillConcurrency)
	}
	if settings.FeatureCurrentMaxAge != 4*time.Minute || settings.FeatureBaselineMaxOffset != 3*time.Minute ||
		settings.FeatureMinimumQuality != 80 || settings.FeatureCalculationDelay != 7*time.Second {
		t.Errorf("feature settings = %s/%s/%d/%s",
			settings.FeatureCurrentMaxAge, settings.FeatureBaselineMaxOffset, settings.FeatureMinimumQuality,
			settings.FeatureCalculationDelay)
	}
}

func TestFromEnvRejectsShortMarketWindow(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "")
	t.Setenv("MARKET_WINDOW_RETENTION_MINUTES", "359")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected market window error")
	}
}

func TestFromEnvRejectsShortBackfillWindow(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "")
	t.Setenv("BACKFILL_LOOKBACK_HOURS", "24")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected backfill lookback error")
	}
}

func TestFromEnvRejectsExcessiveBackfillConcurrency(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "")
	t.Setenv("BACKFILL_CONCURRENCY", "33")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected backfill concurrency error")
	}
}

func TestFromEnvRejectsUnsafeFeatureSettings(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "FEATURE_CURRENT_MAX_AGE_SECONDS", value: "901"},
		{key: "FEATURE_BASELINE_MAX_OFFSET_SECONDS", value: "901"},
		{key: "FEATURE_MINIMUM_QUALITY_SCORE", value: "101"},
		{key: "FEATURE_CALCULATION_DELAY_SECONDS", value: "300"},
	}
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/radar")
			t.Setenv("HTTP_PROXY_URL", "")
			t.Setenv(test.key, test.value)
			if _, err := FromEnv(); err == nil {
				t.Fatalf("expected %s error", test.key)
			}
		})
	}
}

func TestFromEnvRejectsSnapshotAgeAtInterval(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "")
	t.Setenv("SNAPSHOT_MAX_EVENT_AGE_SECONDS", "300")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected snapshot max age error")
	}
}

func TestFromEnvRejectsRotationBeforeStaleTimeout(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "")
	t.Setenv("WS_STALE_AFTER_SECONDS", "120")
	t.Setenv("WS_ROTATE_AFTER_MINUTES", "1")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected websocket duration error")
	}
}

func TestFromEnvRejectsUniverseRatioAboveOneHundred(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "")
	t.Setenv("UNIVERSE_MINIMUM_RATIO_PERCENT", "101")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected universe ratio error")
	}
}

func TestFromEnvRejectsUnsafeRequestWeightSettings(t *testing.T) {
	tests := []struct {
		name      string
		perMinute string
		burst     string
	}{
		{name: "above Binance limit", perMinute: "2401", burst: "50"},
		{name: "burst below one kline request", perMinute: "1800", burst: "9"},
		{name: "burst above budget", perMinute: "20", burst: "21"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost/radar")
			t.Setenv("HTTP_PROXY_URL", "")
			t.Setenv("BINANCE_REQUEST_WEIGHT_PER_MINUTE", test.perMinute)
			t.Setenv("BINANCE_REQUEST_WEIGHT_BURST", test.burst)
			if _, err := FromEnv(); err == nil {
				t.Fatal("expected request weight error")
			}
		})
	}
}

func TestFromEnvRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected DATABASE_URL error")
	}
}

func TestFromEnvRejectsUnsupportedProxy(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "socks5://127.0.0.1:7890")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected HTTP_PROXY_URL error")
	}
}

func TestFromEnvRejectsInvalidListenAddress(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/radar")
	t.Setenv("HTTP_PROXY_URL", "")
	t.Setenv("WEB_LISTEN_ADDR", "localhost")
	if _, err := FromEnv(); err == nil {
		t.Fatal("expected WEB_LISTEN_ADDR error")
	}
}
