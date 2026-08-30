package candidateanalysis

import (
	"encoding/json"
	"fmt"
	"io"
)

func RenderJSON(writer io.Writer, report Report) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func RenderMarkdown(writer io.Writer, report Report) error {
	if _, err := fmt.Fprintf(writer,
		"# R4-A0 候选指标分布报告\n\n- 分析版本：`%s`\n- Feature 版本：`%s`\n- 时间范围：`%s` 至 `%s`\n- 生成时间：`%s`\n\n",
		report.AnalysisVersion, report.FeatureVersion, report.Start.Format("2006-01-02 15:04:05Z07:00"),
		report.End.Format("2006-01-02 15:04:05Z07:00"), report.GeneratedAt.Format("2006-01-02 15:04:05Z07:00"),
	); err != nil {
		return err
	}
	for _, sector := range report.Sectors {
		if _, err := fmt.Fprintf(writer,
			"## %s\n\nFeature 行数：%d；有效窗口：%d；闭合 K 线：%d。\n\n"+
				"| 指标 | 样本 | P25 | P50 | P75 | P90 | P95 | P99 | 最大 | 均值 |\n"+
				"| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n",
			sector.Sector, sector.FeatureRows, sector.FeatureWindows, sector.Klines.Rows,
		); err != nil {
			return err
		}
		metrics := []struct {
			name string
			data Distribution
		}{
			{"15m 收益率（%）", sector.Return15m},
			{"1h 收益率（%）", sector.Return1h},
			{"15m 动量加速度（百分点）", sector.Acceleration15m},
			{"1h 成交额（USD）", sector.RecentQuoteVolume1h},
			{"24h 成交额（USD）", sector.QuoteVolume24h},
			{"15m 上涨宽度（%）", sector.PositiveBreadth15m},
			{"1h 上涨宽度（%）", sector.PositiveBreadth1h},
			{"每窗口 15m P95（%）", sector.CrossSectionalP95Return15m},
			{"每窗口 1h P90（%）", sector.CrossSectionalP90Return1h},
			{"量能/前 20 根中位数", sector.Klines.VolumeExpansion20Median},
			{"距 1h 高点回撤（%）", sector.Klines.DrawdownFrom1hHigh},
			{"连续 15m 收盘上涨次数", sector.Klines.PositiveCloseStreak},
		}
		for _, metric := range metrics {
			if err := renderDistributionRow(writer, metric.name, metric.data); err != nil {
				return err
			}
		}
		candidate := sector.Candidates
		if _, err := fmt.Fprintf(writer,
			"\n候选模拟：15m ≥ max(P%.0f, %.2f%%)，或 1h ≥ max(P%.0f, %.2f%%)；尚未应用流动性门槛。\n\n"+
				"| 候选统计 | 数值 |\n| --- | ---: |\n"+
				"| 每窗口候选 P50 / P90 / P95 / 最大 | %.2f / %.2f / %.2f / %.2f |\n"+
				"| 相邻窗口换手率 P50 / P90 / P95 | %.2f%% / %.2f%% / %.2f%% |\n"+
				"| 唯一候选标的 | %d |\n| 进入 / 退出 / 窗口比较 | %d / %d / %d |\n\n",
			candidate.Return15mPercentile, candidate.Return15mAbsoluteFloor,
			candidate.Return1hPercentile, candidate.Return1hAbsoluteFloor,
			candidate.CountPerWindow.P50, candidate.CountPerWindow.P90, candidate.CountPerWindow.P95, candidate.CountPerWindow.Max,
			candidate.TurnoverPerComparison.P50, candidate.TurnoverPerComparison.P90, candidate.TurnoverPerComparison.P95,
			candidate.UniqueSymbols, candidate.Entries, candidate.Exits, candidate.Comparisons,
		); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer, "## 口径说明"); err != nil {
		return err
	}
	for _, note := range report.Notes {
		if _, err := fmt.Fprintf(writer, "\n- %s\n", note); err != nil {
			return err
		}
	}
	return nil
}

func renderDistributionRow(writer io.Writer, name string, data Distribution) error {
	_, err := fmt.Fprintf(writer, "| %s | %d | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f |\n",
		name, data.Count, data.P25, data.P50, data.P75, data.P90, data.P95, data.P99, data.Max, data.Mean)
	return err
}
