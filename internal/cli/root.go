package cli

import (
	"context"
	"io"

	v1command "binance-monitor/internal/v1/command"
	v2command "binance-monitor/internal/v2/command"

	"github.com/spf13/cobra"
)

func Execute(ctx context.Context, arguments []string, stdout, stderr io.Writer) error {
	root := NewRootCommand(stdout, stderr)
	root.SetArgs(arguments)
	return root.ExecuteContext(ctx)
}

func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	var defaultV1Options v1command.Options
	root := &cobra.Command{
		Use:           "binance-monitor",
		Short:         "Binance USDⓈ-M 市场监控与 Telegram 报告服务",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return v1command.Run(command.Context(), defaultV1Options, stdout, stderr)
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	v1command.BindFlags(root.Flags(), &defaultV1Options)

	root.AddCommand(newV1Command(stdout, stderr))
	root.AddCommand(v2command.NewCommands(stdout, stderr)...)
	return root
}

func newV1Command(stdout, stderr io.Writer) *cobra.Command {
	var options v1command.Options
	command := &cobra.Command{
		Use:   "v1",
		Short: "显式运行原定时涨幅榜服务",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return v1command.Run(command.Context(), options, stdout, stderr)
		},
	}
	v1command.BindFlags(command.Flags(), &options)
	return command
}
