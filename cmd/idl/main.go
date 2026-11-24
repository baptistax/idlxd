package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/baptistax/idl/internal/app"
	"github.com/baptistax/idl/internal/config"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stderr, app.Run)
	cancel()
	if code != 0 {
		os.Exit(code)
	}
}

func run(ctx context.Context, args []string, stderr io.Writer, runner func(context.Context, config.Config) error) int {
	cfg, err := config.ParseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, "Usage: idl <username> | idl status")
		return 2
	}

	if err := runner(ctx, cfg); err != nil {
		return 1
	}
	return 0
}
