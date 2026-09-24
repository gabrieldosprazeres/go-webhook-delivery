package main

import (
	"context"
	"os"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/logging"
	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = os.Stderr.WriteString("api: startup failed\n")
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	if len(args) > 0 && args[0] == "credentials" {
		return runCredentials(parent, args[1:])
	}
	if len(args) > 0 && args[0] == "restore" {
		return runRestore(parent, args[1:])
	}
	if len(args) > 0 && args[0] == "workspace" {
		return runWorkspace(parent, args[1:])
	}
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
	pool, err := openAPIDatabase(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	materials, err := cryptobox.Load(cfg.Profile, cfg.Secrets)
	if err != nil {
		return err
	}
	return serveAPI(ctx, cfg, logger, pool, materials)
}
