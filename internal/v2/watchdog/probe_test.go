package watchdog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPProbeHealthy(t *testing.T) {
	now := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	server := qualityServer(t, http.StatusOK, healthyQuality(now))
	defer server.Close()
	probe := NewHTTPProbe(probeSettings(server.URL))
	probe.now = func() time.Time { return now }
	result := probe.Check(context.Background())
	if !result.Healthy || len(result.Reasons) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPProbeReportsDatabaseNotReady(t *testing.T) {
	server := qualityServer(t, http.StatusServiceUnavailable, `{}`)
	defer server.Close()
	probe := NewHTTPProbe(probeSettings(server.URL))
	result := probe.Check(context.Background())
	if result.Healthy || len(result.Reasons) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPProbeReportsAllStaleAndDegradedComponents(t *testing.T) {
	now := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour).Format(time.RFC3339)
	payload := fmt.Sprintf(`{
		"as_of":%q,
		"backfill":{"status":"DEGRADED","missing_count":2},
		"worker":{"status":"DEGRADED","observed_at":%q,"details":{
			"market_connected":false,"last_market_event":%q,"market_error":"dial failed",
			"snapshot_healthy":false,"last_snapshot":%q,"snapshot_error":"low coverage",
			"analysis_healthy":false,"last_analysis":%q,"analysis_error":"no features",
			"universe_error":"exchangeInfo failed"
		}}
	}`, old, old, old, old, old)
	server := qualityServer(t, http.StatusOK, payload)
	defer server.Close()
	probe := NewHTTPProbe(probeSettings(server.URL))
	probe.now = func() time.Time { return now }
	result := probe.Check(context.Background())
	if result.Healthy || len(result.Reasons) < 10 {
		t.Fatalf("result = %#v", result)
	}
}

func probeSettings(url string) Settings {
	return Settings{
		APIBaseURL: url, RequestTimeout: time.Second,
		HeartbeatMaxAge: 90 * time.Second, MarketMaxAge: 2 * time.Minute, DataMaxAge: 10 * time.Minute,
	}
}

func healthyQuality(now time.Time) string {
	value := now.Add(-30 * time.Second).Format(time.RFC3339)
	data := now.Add(-5 * time.Minute).Format(time.RFC3339)
	return fmt.Sprintf(`{
		"as_of":%q,
		"backfill":{"status":"SUCCEEDED","missing_count":0},
		"worker":{"status":"HEALTHY","observed_at":%q,"details":{
			"market_connected":true,"last_market_event":%q,
			"snapshot_healthy":true,"last_snapshot":%q,
			"analysis_healthy":true,"last_analysis":%q
		}}
	}`, data, value, value, data, data)
}

func qualityServer(t *testing.T, readyStatus int, quality string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/health/ready":
			response.WriteHeader(readyStatus)
			_, _ = response.Write([]byte(`{"status":"ready"}`))
		case "/api/v2/quality":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(quality))
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
}
