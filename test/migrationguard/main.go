// Command migrationguard refuses destructive migration downgrade while tenant data exists.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "migration guard:", err)
		os.Exit(1)
	}
}

func run() error {
	databaseURL := os.Getenv("WDE_MIGRATOR_DATABASE_URL")
	target, targetErr := strconv.Atoi(os.Getenv("WDE_ROLLBACK_TARGET"))
	if databaseURL == "" || targetErr != nil || target < 0 || target > 19 {
		return errors.New("database URL and rollback target 0..19 are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer func() { _ = connection.Close(context.Background()) }()
	tx, err := connection.Begin(ctx)
	if err != nil {
		return errors.New("cannot start guarded inspection")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE wde_owner`); err != nil {
		return errors.New("cannot assume migration owner")
	}
	var schema int
	var defaultControl bool
	err = tx.QueryRow(ctx, `SELECT wde.schema_version(),count(*)=1 AND bool_and(state='ready'
		AND generation=0 AND audit_retention_days=365 AND quarantined_at IS NULL AND reconciled_at IS NULL)
		FROM wde.restore_control`).Scan(&schema, &defaultControl)
	if err != nil {
		return errors.New("cannot inspect guarded schema")
	}
	if _, err = tx.Exec(ctx, `SET LOCAL ROLE wde_maintenance_executor`); err != nil {
		return errors.New("cannot assume maintenance reader")
	}
	var operational bool
	err = tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM wde.workspaces UNION ALL SELECT 1 FROM wde.api_keys
		UNION ALL SELECT 1 FROM wde.api_key_scopes UNION ALL SELECT 1 FROM wde.endpoints
		UNION ALL SELECT 1 FROM wde.endpoint_subscriptions UNION ALL SELECT 1 FROM wde.endpoint_runtime
		UNION ALL SELECT 1 FROM wde.endpoint_secret_versions UNION ALL SELECT 1 FROM wde.events
		UNION ALL SELECT 1 FROM wde.deliveries UNION ALL SELECT 1 FROM wde.delivery_attempts
		UNION ALL SELECT 1 FROM wde.audit_events UNION ALL SELECT 1 FROM wde.replay_commands
		UNION ALL SELECT 1 FROM wde.rate_limit_buckets UNION ALL SELECT 1 FROM wde.secret_rotation_commands
		UNION ALL SELECT 1 FROM wde.maintenance_jobs UNION ALL SELECT 1 FROM wde.workspace_tombstones
	)`).Scan(&operational)
	if err != nil {
		return errors.New("cannot inspect operational state")
	}
	if schema != 5 || !defaultControl || operational {
		return fmt.Errorf("refusing downgrade to %d with non-default v5 state", target)
	}
	return nil
}
