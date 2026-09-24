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
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("chaoslab: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
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
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	logger.InfoContext(ctx, "service started", slog.String("component", "http"))
	err = appruntime.Serve(ctx, server, listener, cfg.ShutdownTimeout)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("service stopped", slog.String("component", "http"))
	return nil
}

func routes() http.Handler {
	return chaos.Handler()
}
