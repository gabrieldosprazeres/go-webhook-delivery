package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
)

func startTelemetry(ctx context.Context, cfg config.Config, logger *slog.Logger) *telemetry.Service {
	service, err := telemetry.New(ctx, telemetry.Options{Service: string(cfg.Service), Version: cfg.Version,
		Environment: string(cfg.Profile), OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
		ExportTimeout: cfg.Telemetry.ExportTimeout, TraceSampleRatio: cfg.Telemetry.TraceSampleRatio})
	if err != nil {
		logger.WarnContext(ctx, "telemetry exporter unavailable", slog.String("code", "exporter_init"))
	}
	return service
}

type telemetryShutdowner interface{ Shutdown(context.Context) error }

func stopTelemetry(service telemetryShutdowner, budget *appruntime.ShutdownBudget,
	timeout time.Duration, logger *slog.Logger,
) {
	ctx, cancel := budget.Context(timeout)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		logger.Warn("telemetry flush incomplete", slog.String("code", "exporter_shutdown"))
	}
}
