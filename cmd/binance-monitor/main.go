package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata"

	"binance-monitor/internal/cli"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	return cli.Execute(ctx, arguments, os.Stdout, os.Stderr)
}
