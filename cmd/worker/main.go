package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/logging"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
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
	listener, err := net.Listen("tcp", cfg.OperationalAddr)
	if err != nil {
		return err
	}
	readiness := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
			return database.ErrUnavailable
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
	if err := appruntime.Serve(ctx, server, listener, cfg.ShutdownTimeout); err != nil {
		return err
	}
	logger.Info("service stopped", slog.String("component", "scheduler"))
	return nil
}
