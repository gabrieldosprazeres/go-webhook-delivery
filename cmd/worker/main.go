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
	runner := delivery.NewRunnerWithOptions(delivery.NewPostgresStore(pool), materials, cfg.Profile,
		cfg.AllowHTTPDestinations, workerID, delivery.RunnerOptions{
			Poll: cfg.WorkerPollInterval, ClaimTimeout: cfg.WorkerClaimTimeout,
			RequestTimeout: cfg.WorkerRequestTimeout,
			LeaseTTL:       cfg.WorkerLeaseTTL, Shutdown: cfg.ShutdownTimeout,
			Concurrency: cfg.WorkerConcurrency, BatchSize: cfg.WorkerClaimBatchSize,
			WorkspaceLimit: cfg.WorkerWorkspaceLimit, EndpointLimit: cfg.WorkerEndpointLimit,
			Retry: delivery.RetryPolicy{Base: cfg.WorkerRetryBase, Cap: cfg.WorkerRetryCap,
				Jitter: delivery.DefaultRetryPolicy().Jitter},
		})
	retentionRunner := retention.NewRunner(retention.NewPostgresStore(pool), time.Hour).
		WithObserver(retentionObserver(ctx, logger))
	listener, err := net.Listen("tcp", cfg.OperationalAddr)
	if err != nil {
		return err
	}
	readiness := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
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
		Handler:           operational.Handler(readiness),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}

	logger.InfoContext(ctx, "service started", slog.String("component", "scheduler"))
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return appruntime.Serve(groupCtx, server, listener, cfg.ShutdownTimeout) })
	group.Go(func() error { return runner.Run(groupCtx) })
	group.Go(func() error { return retentionRunner.Run(groupCtx) })
	if err := group.Wait(); err != nil {
		return err
	}
	logger.Info("service stopped", slog.String("component", "scheduler"))
	return nil
}

func retentionObserver(ctx context.Context, logger *slog.Logger) func(retention.Report) {
	return func(report retention.Report) {
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
