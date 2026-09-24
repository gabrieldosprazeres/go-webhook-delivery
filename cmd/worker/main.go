package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/logging"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/telemetry"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/retention"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("worker: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	if len(args) == 1 && args[0] == "healthcheck" {
		return operational.CheckReady(parent, environmentAddress("WDE_WORKER_OPERATIONAL_ADDR", "127.0.0.1:9091"))
	}
	cfg, err := config.Load(config.LoadOptions{Service: config.ServiceWorker, Args: args})
	if err != nil {
		return err
	}
	logger := logging.NewJSON(os.Stdout, logging.Options{
		Service:     string(cfg.Service),
		Version:     cfg.Version,
		Environment: string(cfg.Profile),
		Level:       cfg.LogLevel,
	})
	ctx, stop := appruntime.SignalContext(parent)
	defer stop()
	shutdown := appruntime.NewShutdownBudget(cfg.ShutdownTimeout)
	observability := startTelemetry(ctx, cfg, logger)
	defer stopTelemetry(observability, shutdown, cfg.Telemetry.ExportTimeout, logger)
	databaseCtx, cancelDatabase := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	pool, err := database.Open(databaseCtx, cfg.DatabaseURL, database.RoleWorker)
	cancelDatabase()
	if err != nil {
		return err
	}
	defer pool.Close()
	materials, err := cryptobox.Load(cfg.Profile, cfg.Secrets)
	if err != nil {
		return err
	}
	workerID, err := uuid.NewV7()
	if err != nil {
		return err
	}
	deliveryStore := delivery.NewPostgresStore(pool)
	deliveryObserver := &deliveryTelemetry{service: observability}
	runner := delivery.NewRunnerWithOptions(deliveryStore, materials, cfg.Profile,
		cfg.AllowHTTPDestinations, workerID, delivery.RunnerOptions{
			Poll: cfg.WorkerPollInterval, ClaimTimeout: cfg.WorkerClaimTimeout,
			RequestTimeout: cfg.WorkerRequestTimeout,
			LeaseTTL:       cfg.WorkerLeaseTTL, Shutdown: cfg.ShutdownTimeout,
			Concurrency: cfg.WorkerConcurrency, BatchSize: cfg.WorkerClaimBatchSize,
			WorkspaceLimit: cfg.WorkerWorkspaceLimit, EndpointLimit: cfg.WorkerEndpointLimit,
			Retry: delivery.RetryPolicy{Base: cfg.WorkerRetryBase, Cap: cfg.WorkerRetryCap,
				Jitter: delivery.DefaultRetryPolicy().Jitter}, Observer: deliveryObserver,
		})
	retentionRunner := retention.NewRunner(retention.NewPostgresStore(pool), time.Hour).
		WithObserver(retentionObserver(ctx, logger, observability.Metrics()))
	listener, err := net.Listen("tcp", cfg.OperationalAddr)
	if err != nil {
		return err
	}
	readiness := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
			return database.ErrUnavailable
		}
		if err := materials.Ready(); err != nil {
			return database.ErrUnavailable
		}
		if !deliveryObserver.Active() {
			return database.ErrUnavailable
		}
		if err := retentionRunner.Ready(); err != nil {
			return err
		}
		checkCtx, cancel := context.WithTimeout(requestCtx, cfg.DatabaseTimeout)
		defer cancel()
		return database.Check(checkCtx, pool, database.RoleWorker)
	}
	server := &http.Server{
		Handler:           operational.Handler(readiness, observability.Metrics().Handler()),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}

	logger.InfoContext(ctx, "service started", slog.String("component", "scheduler"))
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return appruntime.ServeWithBudget(groupCtx, server, listener, shutdown) })
	group.Go(func() error { return runner.Run(groupCtx) })
	group.Go(func() error { return retentionRunner.Run(groupCtx) })
	group.Go(func() error { return runQueueMetrics(groupCtx, deliveryStore, observability.Metrics()) })
	if err := group.Wait(); err != nil {
		return err
	}
	logger.Info("service stopped", slog.String("component", "scheduler"))
	return nil
}

func environmentAddress(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func retentionObserver(ctx context.Context, logger *slog.Logger, metrics *telemetry.Metrics) func(retention.Report) {
	return func(report retention.Report) {
		observeRetention(metrics, report)
		level := slog.LevelInfo
		if report.Degraded {
			level = slog.LevelWarn
		}
		logger.LogAttrs(ctx, level, "retention maintenance completed",
			slog.Int64("backlog", report.Backlog),
			slog.Duration("oldest_age", report.OldestAge),
			slog.Duration("duration", report.Duration),
			slog.Int("batches", report.Batches),
			slog.Bool("degraded", report.Degraded),
			slog.Group("deleted",
				slog.Int64("payloads", report.Counts.Payloads),
				slog.Int64("secrets", report.Counts.Secrets),
				slog.Int64("attempts", report.Counts.Attempts),
				slog.Int64("deliveries", report.Counts.Deliveries),
				slog.Int64("events", report.Counts.Events),
				slog.Int64("audits", report.Counts.Audits),
				slog.Int64("buckets", report.Counts.Buckets),
				slog.Int64("workspace_rows", report.Counts.WorkspaceRows)))
	}
}

func observeRetention(metrics *telemetry.Metrics, report retention.Report) {
	counts := map[string]int64{
		"payload": report.Counts.Payloads, "secret": report.Counts.Secrets,
		"attempt": report.Counts.Attempts, "replay": report.Counts.Replays,
		"rotation": report.Counts.Rotations, "delivery": report.Counts.Deliveries,
		"event": report.Counts.Events, "audit": report.Counts.Audits,
		"bucket": report.Counts.Buckets, "workspace": report.Counts.WorkspaceRows,
	}
	for category, count := range counts {
		metrics.ObservePurge(category, count)
	}
	metrics.SetPurgeLag(report.OldestAge)
}
