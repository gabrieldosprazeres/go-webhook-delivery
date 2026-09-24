package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/retention"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runRestore(ctx context.Context, args []string) error {
	if len(args) == 0 || (args[0] != "quarantine" && args[0] != "reconcile") {
		return errors.New("restore: quarantine or reconcile command required")
	}
	flags := flag.NewFlagSet("restore quarantine", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	generation := flags.Int64("generation", 0, "monotonic restore generation")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *generation <= 0 {
		return errors.New("restore: --generation must be positive")
	}
	pool, err := openRawAdminPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := retention.NewAdminStore(pool)
	if args[0] == "reconcile" {
		_, err = store.CompleteRestore(ctx, *generation)
	} else {
		_, err = store.QuarantineRestore(ctx, *generation)
	}
	return err
}

func runWorkspace(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "purge" {
		return errors.New("workspace: purge command required")
	}
	flags := flag.NewFlagSet("workspace purge", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	idValue := flags.String("id", "", "workspace UUID")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return errors.New("workspace: invalid arguments")
	}
	id, err := uuid.Parse(*idValue)
	if err != nil {
		return errors.New("workspace: --id must be a UUID")
	}
	pool, err := openRawAdminPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	_, err = retention.NewAdminStore(pool).RequestWorkspacePurge(ctx, id, "admin-cli")
	return err
}

func openRawAdminPool(ctx context.Context) (*pgxpool.Pool, error) {
	lookup := func(key string) (string, bool) {
		if key == "WDE_DATABASE_URL" {
			if value, ok := os.LookupEnv("WDE_ADMIN_DATABASE_URL"); ok {
				return value, true
			}
		}
		return os.LookupEnv(key)
	}
	cfg, err := config.Load(config.LoadOptions{Service: config.ServiceAPI, LookupEnv: lookup})
	if err != nil {
		return nil, err
	}
	databaseCtx, cancel := context.WithTimeout(ctx, cfg.DatabaseTimeout)
	defer cancel()
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, database.ErrConfiguration
	}
	poolCfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(databaseCtx, poolCfg)
	if err != nil {
		return nil, database.ErrUnavailable
	}
	var role string
	var version int
	if err := pool.QueryRow(databaseCtx, `SELECT current_user::text,wde.schema_version()`).Scan(&role, &version); err != nil || role != database.RoleAdmin || version != database.SchemaVersion {
		pool.Close()
		return nil, database.ErrConfiguration
	}
	return pool, nil
}
