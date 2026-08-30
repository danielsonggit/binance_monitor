package watchdog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxProbeBody = 1 << 20

type ProbeResult struct {
	CheckedAt time.Time
	Healthy   bool
	Reasons   []string
}

func (r ProbeResult) Reason() string {
	return strings.Join(r.Reasons, "；")
}

type Probe interface {
	Check(context.Context) ProbeResult
}

type HTTPProbe struct {
	baseURL         string
	client          *http.Client
	heartbeatMaxAge time.Duration
	marketMaxAge    time.Duration
	dataMaxAge      time.Duration
	now             func() time.Time
}

type qualityResponse struct {
	AsOf     time.Time `json:"as_of"`
	Backfill *struct {
		Status       string `json:"status"`
		MissingCount int    `json:"missing_count"`
	} `json:"backfill"`
	Worker *struct {
		Status     string    `json:"status"`
		ObservedAt time.Time `json:"observed_at"`
		Details    struct {
			MarketConnected bool      `json:"market_connected"`
			LastMarketEvent time.Time `json:"last_market_event"`
			MarketError     string    `json:"market_error"`
			SnapshotHealthy bool      `json:"snapshot_healthy"`
			LastSnapshot    time.Time `json:"last_snapshot"`
			SnapshotError   string    `json:"snapshot_error"`
			AnalysisHealthy bool      `json:"analysis_healthy"`
			LastAnalysis    time.Time `json:"last_analysis"`
			AnalysisError   string    `json:"analysis_error"`
			UniverseError   string    `json:"universe_error"`
		} `json:"details"`
	} `json:"worker"`
}

func NewHTTPProbe(settings Settings) *HTTPProbe {
	return &HTTPProbe{
		baseURL:         settings.APIBaseURL,
		client:          &http.Client{Timeout: settings.RequestTimeout},
		heartbeatMaxAge: settings.HeartbeatMaxAge,
		marketMaxAge:    settings.MarketMaxAge,
		dataMaxAge:      settings.DataMaxAge,
		now:             func() time.Time { return time.Now().UTC() },
	}
}

func (p *HTTPProbe) Check(ctx context.Context) ProbeResult {
	now := p.now().UTC()
	result := ProbeResult{CheckedAt: now}
	if err := p.getJSON(ctx, "/health/ready", nil); err != nil {
		result.Reasons = append(result.Reasons, "API/数据库未就绪: "+err.Error())
		return result
	}
	var quality qualityResponse
	if err := p.getJSON(ctx, "/api/v2/quality", &quality); err != nil {
		result.Reasons = append(result.Reasons, "质量 API 不可用: "+err.Error())
		return result
	}
	if quality.Worker == nil {
		result.Reasons = append(result.Reasons, "缺少 worker heartbeat")
		return result
	}
	worker := quality.Worker
	if worker.Status != "HEALTHY" {
		result.Reasons = append(result.Reasons, "worker 状态="+worker.Status)
	}
	appendStaleReason(&result.Reasons, "worker heartbeat", now, worker.ObservedAt, p.heartbeatMaxAge)
	if !worker.Details.MarketConnected {
		result.Reasons = append(result.Reasons, detailReason("WebSocket 未连接", worker.Details.MarketError))
	}
	appendStaleReason(&result.Reasons, "最近市场事件", now, worker.Details.LastMarketEvent, p.marketMaxAge)
	if !worker.Details.SnapshotHealthy {
		result.Reasons = append(result.Reasons, detailReason("快照不健康", worker.Details.SnapshotError))
	}
	appendStaleReason(&result.Reasons, "最近快照", now, worker.Details.LastSnapshot, p.dataMaxAge)
	if !worker.Details.AnalysisHealthy {
		result.Reasons = append(result.Reasons, detailReason("分析不健康", worker.Details.AnalysisError))
	}
	appendStaleReason(&result.Reasons, "最近分析", now, worker.Details.LastAnalysis, p.dataMaxAge)
	appendStaleReason(&result.Reasons, "最新质量数据", now, quality.AsOf, p.dataMaxAge)
	if worker.Details.UniverseError != "" {
		result.Reasons = append(result.Reasons, "合约目录异常: "+worker.Details.UniverseError)
	}
	if quality.Backfill == nil {
		result.Reasons = append(result.Reasons, "缺少 backfill 状态")
	} else if quality.Backfill.Status != "SUCCEEDED" || quality.Backfill.MissingCount != 0 {
		result.Reasons = append(result.Reasons, fmt.Sprintf(
			"backfill 状态=%s missing=%d", quality.Backfill.Status, quality.Backfill.MissingCount,
		))
	}
	result.Healthy = len(result.Reasons) == 0
	return result
}

func (p *HTTPProbe) getJSON(ctx context.Context, path string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return err
	}
	response, err := p.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxProbeBody))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if output != nil {
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("解析 JSON: %w", err)
		}
	}
	return nil
}

func appendStaleReason(reasons *[]string, name string, now, observedAt time.Time, maximumAge time.Duration) {
	if observedAt.IsZero() {
		*reasons = append(*reasons, name+"缺失")
		return
	}
	age := now.Sub(observedAt)
	if age > maximumAge {
		*reasons = append(*reasons, fmt.Sprintf("%s过期 %.0f 秒", name, age.Seconds()))
	}
}

func detailReason(summary, detail string) string {
	if strings.TrimSpace(detail) == "" {
		return summary
	}
	return summary + ": " + detail
}
