package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQueueMetricsDowngradeGuardIsAtomicAndSerialized(t *testing.T) {
	scenarios := []struct {
		name  string
		seed  func(*testing.T, context.Context, *pgxpool.Pool)
		check func(*testing.T, context.Context, *pgxpool.Pool)
	}{
		{name: "workspace", seed: seedRollbackWorkspace, check: func(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
			assertCount(t, ctx, db, "wde.workspaces", 1)
		}},
		{name: "global_restore_quarantine", seed: seedRollbackRestore, check: func(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
			assertCount(t, ctx, db, "wde.audit_events", 1)
			var state string
			if err := db.QueryRow(ctx, `SELECT state FROM wde.restore_control WHERE singleton`).Scan(&state); err != nil || state != "quarantined" {
				t.Fatalf("restore state=%q err=%v", state, err)
			}
		}},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, db, root, databaseURL := newRollbackBoundary(t)
			scenario.seed(t, ctx, db)
			if output, err := runGooseResult(ctx, root, databaseURL, "down-to", 19); err == nil {
				t.Fatalf("unsafe downgrade passed: %s", output)
			}
			assertQueueMetricsBoundaryIntact(t, ctx, db)
			scenario.check(t, ctx, db)
		})
	}
	t.Run("concurrent_global_audit", testConcurrentRollbackWriter)
}

func TestConsoleDowngradeGuardIsAtomicAndSerialized(t *testing.T) {
	ctx, db, root, databaseURL := newRollbackBoundaryAt(t, 21)
	workspaceID, keyID := uuid.New(), uuid.New()
	if _, err := db.Exec(ctx, `INSERT INTO wde.workspaces(id,name)
		VALUES($1,'console-rollback-guard')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier)
		VALUES($1,$2,'console123456789',$3)`, keyID, workspaceID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `INSERT INTO wde.console_sessions(
		id,workspace_id,api_key_id,token_prefix,token_verifier,idle_expires_at,
		absolute_expires_at,restore_generation)
		VALUES($1,$2,$3,'0123456789abcdef',$4,clock_timestamp()+interval '15 minutes',
		clock_timestamp()+interval '60 minutes',0)`, uuid.New(), workspaceID, keyID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		_, runErr := runGooseResult(ctx, root, databaseURL, "down-to", 20)
		result <- runErr
	}()
	select {
	case err = <-result:
		t.Fatalf("downgrade did not wait for active console writer: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err == nil {
		t.Fatal("downgrade missed concurrently committed console session")
	}

	var schemaVersion, migrationVersion, sessions int
	if err = db.QueryRow(ctx, `SELECT wde.schema_version(),
		(SELECT max(version_id) FROM goose_db_version WHERE is_applied),
		(SELECT count(*) FROM wde.console_sessions)`).
		Scan(&schemaVersion, &migrationVersion, &sessions); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 6 || migrationVersion != 21 || sessions != 1 {
		t.Fatalf("schema=%d migration=%d sessions=%d", schemaVersion, migrationVersion, sessions)
	}
}

func testConcurrentRollbackWriter(t *testing.T) {
	ctx, db, root, databaseURL := newRollbackBoundary(t)
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	insertGlobalRestoreAudit(t, ctx, tx)
	result := make(chan error, 1)
	go func() {
		_, runErr := runGooseResult(ctx, root, databaseURL, "down-to", 19)
		result <- runErr
	}()
	select {
	case err = <-result:
		t.Fatalf("downgrade did not wait for active writer: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err == nil {
		t.Fatal("downgrade missed concurrently committed audit event")
	}
	assertQueueMetricsBoundaryIntact(t, ctx, db)
	assertCount(t, ctx, db, "wde.audit_events", 1)
}

func newRollbackBoundary(t *testing.T) (context.Context, *pgxpool.Pool, string, string) {
	return newRollbackBoundaryAt(t, 20)
}

func newRollbackBoundaryAt(t *testing.T, version int) (context.Context, *pgxpool.Pool, string, string) {
	t.Helper()
	superURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL")
	if superURL == "" {
		t.Skip("rollback integration database URL is not configured")
	}
	ctx := context.Background()
	admin := mustPool(t, ctx, superURL)
	name := "wde_rollback_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+identifier+" OWNER wde_owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "REVOKE ALL ON DATABASE "+identifier+" FROM PUBLIC; GRANT CONNECT ON DATABASE "+identifier+
		" TO wde_migrator,wde_api,wde_worker,wde_admin"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dropBoundaryDatabase(t, ctx, admin, name, identifier); admin.Close() })
	boundaryURL := databaseURL(t, superURL, name)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	runGooseBoundary(t, ctx, root, boundaryURL, "up-to", version)
	db := mustPool(t, ctx, boundaryURL)
	t.Cleanup(db.Close)
	return ctx, db, root, boundaryURL
}

func seedRollbackWorkspace(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	if _, err := db.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'rollback-guard')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
}

func seedRollbackRestore(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	if _, err := db.Exec(ctx, `SELECT wde.enter_restore_quarantine(11)`); err != nil {
		t.Fatal(err)
	}
}

func insertGlobalRestoreAudit(t *testing.T, ctx context.Context, executor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}) {
	t.Helper()
	_, err := executor.Exec(ctx, `INSERT INTO wde.audit_events(id,workspace_id,actor_type,actor_id,action,
		resource_type,resource_id,request_id,outcome,reason) VALUES($1,NULL,'system','restore',
		'restore.quarantine','workspace','13','race','completed','serialized rollback guard')`, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
}

func runGooseResult(ctx context.Context, root, databaseURL, direction string, version int) (string, error) {
	command := exec.CommandContext(ctx, "go", "tool", "goose", "-dir", filepath.Join(root, "db/migrations"),
		"postgres", databaseURL, direction, fmt.Sprint(version))
	command.Dir = root
	output, err := command.CombinedOutput()
	return string(output), err
}

func assertQueueMetricsBoundaryIntact(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	t.Helper()
	var schema, physical int
	var metrics bool
	err := db.QueryRow(ctx, `SELECT wde.schema_version(),
		(SELECT max(version_id) FROM goose_db_version WHERE is_applied),
		to_regprocedure('wde.delivery_queue_metrics()') IS NOT NULL`).Scan(&schema, &physical, &metrics)
	if err != nil || schema != 5 || physical != 20 || !metrics {
		t.Fatalf("schema=%d physical=%d metrics=%v err=%v", schema, physical, metrics, err)
	}
}

func assertCount(t *testing.T, ctx context.Context, db *pgxpool.Pool, table string, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != expected {
		t.Fatalf("%s count=%d err=%v", table, count, err)
	}
}
