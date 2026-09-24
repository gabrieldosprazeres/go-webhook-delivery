// Package database owns PostgreSQL connection setup and startup compatibility checks.
package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// SchemaVersion is the only migration version this revision of the binaries accepts.
	SchemaVersion = 1
	RoleAPI       = "wde_api"
	RoleWorker    = "wde_worker"
)

var (
	ErrConfiguration      = errors.New("database configuration is invalid")
	ErrUnavailable        = errors.New("database is unavailable")
	ErrUnexpectedRole     = errors.New("database role is unexpected")
	ErrIncompatibleSchema = errors.New("database schema is incompatible")
)

type healthChecker interface {
	Ping(context.Context) error
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Open creates a bounded pool and verifies connectivity, role and migration compatibility.
func Open(ctx context.Context, databaseURL, expectedRole string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, ErrConfiguration
	}
	poolConfig.MaxConns = 5
	poolConfig.MinConns = 0
	poolConfig.MaxConnLifetimeJitter = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := Check(ctx, pool, expectedRole); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Check confirms the connection still uses the expected least-privilege role and schema.
func Check(ctx context.Context, checker healthChecker, expectedRole string) error {
	if err := checker.Ping(ctx); err != nil {
		return ErrUnavailable
	}
	var role string
	var schemaVersion int
	if err := checker.QueryRow(ctx, `SELECT current_user::text, wde.schema_version()`).Scan(&role, &schemaVersion); err != nil {
		return ErrUnavailable
	}
	if role != expectedRole {
		return ErrUnexpectedRole
	}
	if schemaVersion != SchemaVersion {
		return ErrIncompatibleSchema
	}
	return nil
}
