package command

import (
	"fmt"
	"io"
	"strings"
	"time"

	"binance-monitor/internal/candidateanalysis"
	"binance-monitor/internal/domain/market"
	"binance-monitor/internal/storage/postgres"

	"github.com/spf13/cobra"
)

func newCandidateAnalysisCommand(stdout, stderr io.Writer) *cobra.Command {
	var opts options
	var endRaw string
	var lookback time.Duration
	var outputFormat string
	command := &cobra.Command{
		Use:   "candidate-analysis",
		Short: "只读分析 R4 候选指标分布与冷启动候选数量",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, _, err := loadSettings(opts, stderr)
			if err != nil {
				return err
			}
			var end time.Time
			if strings.TrimSpace(endRaw) != "" {
				end, err = time.Parse(time.RFC3339, endRaw)
				if err != nil {
					return fmt.Errorf("--end 必须是 RFC3339 时间: %w", err)
				}
				end = end.UTC()
			}
			format := strings.ToLower(strings.TrimSpace(outputFormat))
			if format != "markdown" && format != "json" {
				return fmt.Errorf("--format 只支持 markdown 或 json")
			}
			resources, err := postgres.OpenResources(command.Context(), settings.DatabaseURL, settings.DatabaseMaxConns)
			if err != nil {
				return err
			}
			defer resources.Close()
			service, err := candidateanalysis.NewService(
				postgres.NewCandidateAnalysisRepository(resources.Pool),
				candidateanalysis.SystemClock{},
			)
			if err != nil {
				return err
			}
			report, err := service.Run(command.Context(), candidateanalysis.Options{
				End: end, Lookback: lookback, FeatureVersion: market.ReturnFeatureVersion1,
			})
			if err != nil {
				return err
			}
			if format == "json" {
				return candidateanalysis.RenderJSON(stdout, report)
			}
			return candidateanalysis.RenderMarkdown(stdout, report)
		},
	}
	bindFlags(command, &opts)
	command.Flags().StringVar(&endRaw, "end", "", "分析结束 UTC RFC3339 五分钟时点；默认数据库最新完整时点")
	command.Flags().DurationVar(&lookback, "lookback", 7*24*time.Hour, "分析回看范围（1h 至 336h）")
	command.Flags().StringVar(&outputFormat, "format", "markdown", "输出格式：markdown 或 json")
	return command
}
