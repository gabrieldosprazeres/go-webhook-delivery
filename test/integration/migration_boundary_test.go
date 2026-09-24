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
	claimV2    = "wde.claim_delivery(uuid,uuid,interval)"
	claimV3    = "wde.claim_deliveries(uuid,uuid[],interval,integer,integer,integer)"
	claimStage = "wde.claim_deliveries_v3_stage(uuid,uuid[],interval,integer,integer,integer)"
	finalizeV2 = "wde.finalize_delivery(uuid,uuid,uuid,bigint,boolean,smallint,integer,text)"
	finalizeV3 = "wde.finalize_delivery(uuid,uuid,uuid,bigint,text,smallint,integer,text,interval)"
	finalStage = "wde.finalize_delivery_v3_stage(uuid,uuid,uuid,bigint,text,smallint,integer,text,interval)"
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

	for _, version := range []int{3, 4, 5, 6} {
		runGooseBoundary(t, ctx, root, boundarySuper, "up-to", version)
		assertMigrationBoundary(t, ctx, boundarySuper, boundaryWorker, version)
	}
	for _, version := range []int{5, 4, 3, 2} {
		runGooseBoundary(t, ctx, root, boundarySuper, "down-to", version)
		assertMigrationBoundary(t, ctx, boundarySuper, boundaryWorker, version)
	}

	// Prova que o downgrade completo para o contrato v2 permanece reversivel:
	// o runtime v3 falha fechado em v2 e volta a iniciar somente quando toda a
	// cadeia 000003..000006 e reaplicada atomicamente.
	runGooseBoundary(t, ctx, root, boundarySuper, "up-to", 6)
	assertMigrationBoundary(t, ctx, boundarySuper, boundaryWorker, 6)
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
	if physical == 6 {
		logical = 3
	}
	var schemaVersion, gooseVersion int
	if err := super.QueryRow(ctx, `SELECT wde.schema_version(),
		(SELECT max(version_id) FROM goose_db_version WHERE is_applied)`).Scan(&schemaVersion, &gooseVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != logical || gooseVersion != physical {
		t.Fatalf("boundary=%d schema=%d goose=%d", physical, schemaVersion, gooseVersion)
	}

	if physical == 6 {
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

	worker := mustPool(t, ctx, workerURL)
	defer worker.Close()
	err := database.Check(ctx, worker, database.RoleWorker)
	if physical == 6 && err != nil {
		t.Fatalf("v3 runtime rejected complete boundary: %v", err)
	}
	if physical != 6 && !errors.Is(err, database.ErrIncompatibleSchema) {
		t.Fatalf("v3 runtime did not fail closed at boundary %d: %v", physical, err)
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
