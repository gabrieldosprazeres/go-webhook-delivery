package main

import (
	"context"
	"os"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/logging"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("console: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	if len(args) == 1 && args[0] == "healthcheck" {
		return operational.CheckReady(parent, environmentAddress("WDE_CONSOLE_OPERATIONAL_ADDR", "127.0.0.1:9092"))
	}
	cfg, err := config.Load(config.LoadOptions{Service: config.ServiceConsole, Args: args})
	if err != nil {
		return err
	}
	logger := logging.NewJSON(os.Stdout, logging.Options{Service: string(cfg.Service), Version: cfg.Version,
		Environment: string(cfg.Profile), Level: cfg.LogLevel})
	ctx, stop := appruntime.SignalContext(parent)
	defer stop()
	shutdown := appruntime.NewShutdownBudget(cfg.ShutdownTimeout)
	observability := startTelemetry(ctx, cfg, logger)
	defer stopTelemetry(observability, shutdown, cfg.Telemetry.ExportTimeout, logger)
	databaseCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	pool, err := openConsoleDatabase(databaseCtx, cfg)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	materials, err := cryptobox.Load(cfg.Profile, cfg.Secrets)
	if err != nil {
		return err
	}
	return serveConsole(ctx, cfg, logger, pool, materials, observability, shutdown)
}

func environmentAddress(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
