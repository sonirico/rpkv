// Command rpkv is the process entry point: wiring owner, and nothing but
// wiring.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/sonirico/rpkv/clock"
	"github.com/sonirico/rpkv/internal/app"
	"github.com/sonirico/rpkv/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := config.Parse(os.Args[1:])
	if err != nil {
		logger.Error("parse config", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a, err := app.New(cfg, logger, clock.NewSystem())
	if err != nil {
		logger.Error("build app", "error", err)
		os.Exit(1)
	}

	runErr := a.Run(ctx)
	if closeErr := a.Close(); closeErr != nil {
		logger.Error("close app", "error", closeErr)
	}
	if runErr != nil {
		logger.Error("run app", "error", runErr)
		os.Exit(1)
	}
}
