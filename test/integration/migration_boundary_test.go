package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	claimV2        = "wde.claim_delivery(uuid,uuid,interval)"
	claimV3        = "wde.claim_deliveries(uuid,uuid[],interval,integer,integer,integer)"
	claimStage     = "wde.claim_deliveries_v3_stage(uuid,uuid[],interval,integer,integer,integer)"
	finalizeV2     = "wde.finalize_delivery(uuid,uuid,uuid,bigint,boolean,smallint,integer,text)"
	finalizeV3     = "wde.finalize_delivery(uuid,uuid,uuid,bigint,text,smallint,integer,text,interval)"
	finalStage     = "wde.finalize_delivery_v3_stage(uuid,uuid,uuid,bigint,text,smallint,integer,text,interval)"
	auditV4        = "wde.append_audit_event(uuid,uuid,text,text,text,text,text,text,text,text)"
	auditStage     = "wde.append_audit_event_v4_stage(uuid,uuid,text,text,text,text,text,text,text,text)"
	quotaV4        = "wde.consume_quota(bytea,text,text,uuid,uuid,uuid,integer,integer)"
	quotaStage     = "wde.consume_quota_v4_stage(bytea,text,text,uuid,uuid,uuid,integer,integer)"
	replayV4       = "wde.request_replay(uuid,uuid,uuid,uuid,text,bytea,bytea,smallint,text,text)"
	replayStage    = "wde.request_replay_v4_stage(uuid,uuid,uuid,uuid,text,bytea,bytea,smallint,text,text)"
	rotationV5     = "wde.rotate_endpoint_secret(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer)"
	rotationStage  = "wde.rotate_endpoint_secret_v5_stage(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer)"
	claimV5Stage   = "wde.claim_deliveries_v5_stage(uuid,uuid[],interval,integer,integer,integer)"
	metadataV5     = "wde.purge_expired_metadata(integer)"
	metadataStage  = "wde.purge_expired_metadata_v5_stage(integer)"
	backlogV5      = "wde.retention_backlog()"
	backlogStage   = "wde.retention_backlog_v5_stage()"
	workspaceV5    = "wde.purge_workspace(integer)"
	workspaceStage = "wde.purge_workspace_v5_stage(integer)"
	queueMetricsV5 = "wde.delivery_queue_metrics()"
)

func TestMigrationBoundariesRemainFailClosed(t *testing.T) {
	superURL, workerURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"), os.Getenv("WDE_TEST_WORKER_DATABASE_URL")
	if superURL == "" || workerURL == "" {
		t.Skip("boundary integration database URLs are not configured")
	}
	ctx := context.Background()
	admin := mustPool(t, ctx, superURL)
	defer admin.Close()
	databaseName := "wde_boundary_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+identifier+" OWNER wde_owner"); err != nil {
		t.Fatal(err)
	}
	defer dropBoundaryDatabase(t, ctx, admin, databaseName, identifier)
	if _, err := admin.Exec(ctx, "REVOKE ALL ON DATABASE "+identifier+
		" FROM PUBLIC; GRANT CONNECT ON DATABASE "+identifier+" TO wde_migrator,wde_api,wde_worker,wde_admin"); err != nil {
		t.Fatal(err)
	}
	boundarySuper := databaseURL(t, superURL, databaseName)
	boundaryWorker := databaseURL(t, workerURL, databaseName)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	for _, version := range []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20} {
		runGooseBoundary(t, ctx, root, boundarySuper, "up-to", version)
		assertMigrationBoundary(t, ctx, boundarySuper, boundaryWorker, version)
	}
	for _, version := range []int{19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2} {
		runGooseBoundary(t, ctx, root, boundarySuper, "down-to", version)
		assertMigrationBoundary(t, ctx, boundarySuper, boundaryWorker, version)
	}

	// Prova que o downgrade completo para o contrato v2 permanece reversivel:
	// o runtime v3 falha fechado em v2 e volta a iniciar somente quando toda a
	// cadeia 000003..000006 e reaplicada atomicamente.
	runGooseBoundary(t, ctx, root, boundarySuper, "up-to", 20)
	assertMigrationBoundary(t, ctx, boundarySuper, boundaryWorker, 20)
}

func runGooseBoundary(t *testing.T, ctx context.Context, root, databaseURL, direction string, version int) {
	t.Helper()
	command := exec.CommandContext(ctx, "go", "tool", "goose", "-dir", filepath.Join(root, "db/migrations"),
		"postgres", databaseURL, direction, fmt.Sprint(version))
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("goose %s %d: %v\n%s", direction, version, err, output)
	}
}

func assertMigrationBoundary(t *testing.T, ctx context.Context, superURL, workerURL string, physical int) {
	t.Helper()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	logical := 2
	if physical >= 6 {
		logical = 3
	}
	if physical >= 10 {
		logical = 4
	}
	if physical >= 19 {
		logical = 5
	}
	var schemaVersion, gooseVersion int
	if err := super.QueryRow(ctx, `SELECT wde.schema_version(),
		(SELECT max(version_id) FROM goose_db_version WHERE is_applied)`).Scan(&schemaVersion, &gooseVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != logical || gooseVersion != physical {
		t.Fatalf("boundary=%d schema=%d goose=%d", physical, schemaVersion, gooseVersion)
	}

	if physical >= 6 {
		assertFunctionState(t, ctx, super, claimV2, false, false)
		assertFunctionState(t, ctx, super, finalizeV2, false, false)
		assertFunctionState(t, ctx, super, claimV3, true, true)
		assertFunctionState(t, ctx, super, finalizeV3, true, true)
		assertFunctionState(t, ctx, super, "wde.claim_delivery_v2(uuid,uuid,interval)", true, false)
		assertFunctionState(t, ctx, super,
			"wde.finalize_delivery_v2(uuid,uuid,uuid,bigint,boolean,smallint,integer,text)", true, false)
	} else {
		assertFunctionState(t, ctx, super, claimV2, true, true)
		assertFunctionState(t, ctx, super, finalizeV2, true, true)
		assertFunctionState(t, ctx, super, claimV3, false, false)
		assertFunctionState(t, ctx, super, finalizeV3, false, false)
	}
	assertFunctionState(t, ctx, super, claimStage, physical == 5, false)
	assertFunctionState(t, ctx, super, finalStage, physical >= 3 && physical <= 5, false)
	assertFunctionState(t, ctx, super, auditStage, physical >= 8 && physical <= 9, false)
	assertFunctionState(t, ctx, super, quotaStage, physical >= 8 && physical <= 9, false)
	assertFunctionState(t, ctx, super, replayStage, physical == 9, false)
	assertAPIFunctionState(t, ctx, super, auditV4, physical >= 10, physical >= 10)
	assertAPIFunctionState(t, ctx, super, quotaV4, physical >= 10, physical >= 10)
	assertAPIFunctionState(t, ctx, super, replayV4, physical >= 10, physical >= 10)
	assertAPIFunctionState(t, ctx, super, rotationStage, physical >= 12 && physical <= 18, false)
	assertAPIFunctionState(t, ctx, super, rotationV5, physical >= 19, physical >= 19)
	assertFunctionState(t, ctx, super, claimV5Stage, physical == 18, false)
	assertFunctionState(t, ctx, super, metadataStage, physical >= 14 && physical <= 18, false)
	assertFunctionState(t, ctx, super, metadataV5, physical >= 19, physical >= 19)
	assertFunctionState(t, ctx, super, backlogStage, physical >= 15 && physical <= 18, false)
	assertFunctionState(t, ctx, super, backlogV5, physical >= 19, physical >= 19)
	assertFunctionState(t, ctx, super, workspaceStage, physical >= 17 && physical <= 18, false)
	assertFunctionState(t, ctx, super, workspaceV5, physical >= 19, physical >= 19)
	assertFunctionState(t, ctx, super, queueMetricsV5, physical == 20, physical == 20)

	worker := mustPool(t, ctx, workerURL)
	defer worker.Close()
	err := database.Check(ctx, worker, database.RoleWorker)
	if physical >= 19 && err != nil {
		t.Fatalf("v5 runtime rejected complete boundary: %v", err)
	}
	if physical < 19 && !errors.Is(err, database.ErrIncompatibleSchema) {
		t.Fatalf("v5 runtime did not fail closed at boundary %d: %v", physical, err)
	}
}

func assertAPIFunctionState(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	signature string, wantExists, wantAPIExecute bool,
) {
	t.Helper()
	var exists, apiExecute, publicExecute bool
	err := pool.QueryRow(ctx, `SELECT to_regprocedure($1) IS NOT NULL,
		COALESCE(has_function_privilege('wde_api',to_regprocedure($1),'EXECUTE'),false),
		COALESCE(has_function_privilege('public',to_regprocedure($1),'EXECUTE'),false)`, signature).
		Scan(&exists, &apiExecute, &publicExecute)
	if err != nil {
		t.Fatal(err)
	}
	if exists != wantExists || apiExecute != wantAPIExecute || publicExecute {
		t.Fatalf("%s exists=%v api=%v public=%v want=%v/%v/false",
			signature, exists, apiExecute, publicExecute, wantExists, wantAPIExecute)
	}
}

func assertFunctionState(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	signature string, wantExists, wantWorkerExecute bool,
) {
	t.Helper()
	var exists, workerExecute bool
	err := pool.QueryRow(ctx, `SELECT to_regprocedure($1) IS NOT NULL,
		COALESCE(has_function_privilege('wde_worker',to_regprocedure($1),'EXECUTE'),false)`, signature).
		Scan(&exists, &workerExecute)
	if err != nil {
		t.Fatal(err)
	}
	if exists != wantExists || workerExecute != wantWorkerExecute {
		t.Fatalf("%s exists=%v execute=%v want=%v/%v", signature, exists, workerExecute,
			wantExists, wantWorkerExecute)
	}
}

func databaseURL(t *testing.T, raw, databaseName string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	return parsed.String()
}

func dropBoundaryDatabase(t *testing.T, ctx context.Context, admin *pgxpool.Pool, name, identifier string) {
	t.Helper()
	if _, err := admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname=$1 AND pid<>pg_backend_pid()`, name); err != nil {
		t.Errorf("terminate boundary connections: %v", err)
	}
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+identifier); err != nil {
		t.Errorf("drop boundary database: %v", err)
	}
}
