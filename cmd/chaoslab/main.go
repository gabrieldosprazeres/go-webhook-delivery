package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	chaos "github.com/gabrieldosprazeres/go-webhook-delivery/internal/chaoslab"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/logging"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/operational"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("chaoslab: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	if len(args) == 1 && args[0] == "healthcheck" {
		return operational.CheckLive(parent, environmentAddress("WDE_CHAOSLAB_HTTP_ADDR", "127.0.0.1:8081"))
	}
	cfg, err := config.Load(config.LoadOptions{Service: config.ServiceChaosLab, Args: args})
	if err != nil {
		return err
	}
	logger := logging.NewJSON(os.Stdout, logging.Options{
		Service:     string(cfg.Service),
		Version:     cfg.Version,
		Environment: string(cfg.Profile),
		Level:       cfg.LogLevel,
	})
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return err
	}

	ctx, stop := appruntime.SignalContext(parent)
	defer stop()
	server := &http.Server{
		Handler:           routes(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}

	logger.InfoContext(ctx, "service started", slog.String("component", "http"))
	err = appruntime.Serve(ctx, server, listener, cfg.ShutdownTimeout)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("service stopped", slog.String("component", "http"))
	return nil
}

func environmentAddress(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func routes() http.Handler {
	return chaos.Handler()
}
