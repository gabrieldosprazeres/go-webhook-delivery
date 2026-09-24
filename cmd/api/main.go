package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/logging"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("api: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	cfg, err := config.Load(config.LoadOptions{Service: config.ServiceAPI, Args: args})
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
	pool, err := database.Open(databaseCtx, cfg.DatabaseURL, database.RoleAPI)
	cancelDatabase()
	if err != nil {
		return err
	}
	defer pool.Close()

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}
	operationalListener, err := net.Listen("tcp", cfg.OperationalAddr)
	if err != nil {
		_ = listener.Close()
		return err
	}

	readiness := func(requestCtx context.Context) error {
		if ctx.Err() != nil {
			return database.ErrUnavailable
		}
		checkCtx, cancel := context.WithTimeout(requestCtx, cfg.DatabaseTimeout)
		defer cancel()
		return database.Check(checkCtx, pool, database.RoleAPI)
	}
	server := &http.Server{
		Handler:           publicRoutes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	operationalServer := &http.Server{
		Handler:           operational.Handler(readiness),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}

	logger.InfoContext(ctx, "service started", slog.String("component", "http"))
	err = appruntime.ServeAll(ctx, cfg.ShutdownTimeout,
		appruntime.HTTPServer{Server: server, Listener: listener},
		appruntime.HTTPServer{Server: operationalServer, Listener: operationalListener},
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("service stopped", slog.String("component", "http"))
	return nil
}

func publicRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", problem.NotFound())
	return problem.WithRequestID(mux)
}
