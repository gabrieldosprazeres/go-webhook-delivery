package integration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCredentialBootstrapIsSerializedAndRevocable(t *testing.T) {
	apiURL := os.Getenv("WDE_TEST_API_DATABASE_URL")
	adminURL := os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	if apiURL == "" || adminURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	admin := mustPool(t, ctx, adminURL)
	defer admin.Close()
	api := mustPool(t, ctx, apiURL)
	defer api.Close()
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	store := auth.NewAdminStore(admin, materials.AuthPepper, true)
	failingCtx, cancel := context.WithCancel(ctx)
	persisted := false
	if _, err := store.Bootstrap(failingCtx, "failed-commit", func(auth.BootstrapResult) (func(), error) {
		persisted = true
		cancel()
		return func() { persisted = false }, nil
	}); err == nil {
		t.Fatal("cancelled commit unexpectedly succeeded")
	}
	if persisted {
		t.Fatal("credential sink was not cleaned after commit failure")
	}
	var workspaceCount int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM wde.workspaces`).Scan(&workspaceCount); err != nil || workspaceCount != 0 {
		t.Fatalf("workspaces=%d err=%v", workspaceCount, err)
	}
	type outcome struct {
		result auth.BootstrapResult
		err    error
	}
	outcomes := make(chan outcome, 2)
	var group sync.WaitGroup
	for index := range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := store.Bootstrap(ctx, "bootstrap-"+string(rune('a'+index)), func(result auth.BootstrapResult) (func(), error) { return nil, nil })
			outcomes <- outcome{result, err}
		}()
	}
	group.Wait()
	close(outcomes)
	var winner auth.BootstrapResult
	successes, already := 0, 0
	for item := range outcomes {
		if item.err == nil {
			successes++
			winner = item.result
		} else if errors.Is(item.err, auth.ErrAlreadyBootstrapped) {
			already++
		} else {
			t.Fatal(item.err)
		}
	}
	if successes != 1 || already != 1 {
		t.Fatalf("successes=%d already=%d", successes, already)
	}
	parts := strings.SplitN(winner.Token, "_", 4)
	if len(parts) != 4 {
		t.Fatal("invalid generated token")
	}
	changed, err := store.Revoke(ctx, parts[2])
	if err != nil || !changed {
		t.Fatalf("revoke changed=%v err=%v", changed, err)
	}
	record, found, err := auth.NewPostgresLookup(api).LookupKey(ctx, parts[2])
	if err != nil || !found || record.Status != "revoked" {
		t.Fatalf("found=%v status=%s err=%v", found, record.Status, err)
	}
}

// TestTenantContextAndAppendOnlyACL runs in CI against a freshly migrated PostgreSQL.
// It is skipped for the default unit-test command when integration DSNs are absent.
func TestTenantContextAndAppendOnlyACL(t *testing.T) {
	apiURL := os.Getenv("WDE_TEST_API_DATABASE_URL")
	adminURL := os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	workerURL := os.Getenv("WDE_TEST_WORKER_DATABASE_URL")
	if apiURL == "" || adminURL == "" || workerURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	api := mustPool(t, ctx, apiURL)
	defer api.Close()
	admin := mustPool(t, ctx, adminURL)
	defer admin.Close()
	worker := mustPool(t, ctx, workerURL)
	defer worker.Close()
	assertTenantFunctionACL(t, ctx, admin)
	workspaceID, endpointID := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'rls-test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.endpoints(id,workspace_id,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version) VALUES($1,$2,'http','127.0.0.1',8081,1,$3,$4,1)`, endpointID, workspaceID, make([]byte, 16), make([]byte, 12)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = api.QueryRow(ctx, `SELECT count(*) FROM wde.endpoints`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("query without tenant returned %d rows", count)
	}
	if _, err = api.Exec(ctx, `INSERT INTO wde.delivery_attempts(id,workspace_id,delivery_id,run_number,attempt_number_in_run,attempt_sequence,fencing_token) VALUES($1,$2,$3,1,1,1,1)`, uuid.New(), workspaceID, uuid.New()); err == nil {
		t.Fatal("API direct attempt DML unexpectedly succeeded")
	}
	if _, err = worker.Exec(ctx, `UPDATE wde.delivery_attempts SET state='abandoned'`); err == nil {
		t.Fatal("worker direct attempt DML unexpectedly succeeded")
	}
	if _, err = admin.Exec(ctx, `UPDATE wde.workspaces SET status='suspended',updated_at=clock_timestamp() WHERE id=$1`, workspaceID); err != nil {
		t.Fatal(err)
	}
	suspendedTx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer suspendedTx.Rollback(ctx)
	if _, err = suspendedTx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	if err = suspendedTx.QueryRow(ctx, `SELECT count(*) FROM wde.endpoints`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("suspended workspace returned %d rows", count)
	}
	if _, err = suspendedTx.Exec(ctx, `INSERT INTO wde.endpoints(id,workspace_id,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version) VALUES($1,$2,'http','127.0.0.1',8081,1,$3,$4,1)`, uuid.New(), workspaceID, make([]byte, 16), make([]byte, 12)); err == nil {
		t.Fatal("suspended workspace inserted endpoint")
	}
}

func assertTenantFunctionACL(t *testing.T, ctx context.Context, admin *pgxpool.Pool) {
	t.Helper()
	var publicExecute, apiExecute, workerExecute, adminExecute bool
	err := admin.QueryRow(ctx, `
		SELECT EXISTS (
		           SELECT 1
		             FROM pg_proc p
		             JOIN pg_namespace n ON n.oid = p.pronamespace
		             CROSS JOIN LATERAL aclexplode(p.proacl) acl
		            WHERE n.nspname = 'wde'
		              AND p.proname = 'current_workspace_id'
		              AND acl.grantee = 0
		              AND acl.privilege_type = 'EXECUTE'
		       ),
		       has_function_privilege('wde_api', 'wde.current_workspace_id()', 'EXECUTE'),
		       has_function_privilege('wde_worker', 'wde.current_workspace_id()', 'EXECUTE'),
		       has_function_privilege('wde_admin', 'wde.current_workspace_id()', 'EXECUTE')
	`).Scan(&publicExecute, &apiExecute, &workerExecute, &adminExecute)
	if err != nil {
		t.Fatal(err)
	}
	if publicExecute || !apiExecute || workerExecute || adminExecute {
		t.Fatalf("current_workspace_id ACL: public=%v api=%v worker=%v admin=%v", publicExecute, apiExecute, workerExecute, adminExecute)
	}
}

func TestConcurrentIdempotency(t *testing.T) {
	apiURL := os.Getenv("WDE_TEST_API_DATABASE_URL")
	adminURL := os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	if apiURL == "" || adminURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	api := mustPool(t, ctx, apiURL)
	defer api.Close()
	admin := mustPool(t, ctx, adminURL)
	defer admin.Close()
	workspaceID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'idempotency-test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	endpointService := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials)
	created, err := endpointService.Create(ctx, workspaceID, endpoint.CreateInput{URL: "http://127.0.0.1:18081/success", EventTypes: []string{"invoice.created"}})
	if err != nil {
		t.Fatal(err)
	}
	service := event.NewService(event.NewPostgresStore(api), materials)
	const requests = 100
	ids := make(chan uuid.UUID, requests)
	errorsCh := make(chan error, requests)
	var group sync.WaitGroup
	for range requests {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.Publish(ctx, workspaceID, "same-key", []byte(`{"type":"invoice.created","data":{"amount":10}}`))
			if err != nil {
				errorsCh <- err
				return
			}
			ids <- result.EventID
		}()
	}
	group.Wait()
	close(ids)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	var expected uuid.UUID
	for id := range ids {
		if expected == uuid.Nil {
			expected = id
		}
		if id != expected {
			t.Fatalf("event ids differ: %s and %s", expected, id)
		}
	}
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	var eventCount, deliveryCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM wde.events WHERE workspace_id=$1`, workspaceID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM wde.deliveries WHERE workspace_id=$1`, workspaceID).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || deliveryCount != 1 {
		t.Fatalf("events=%d deliveries=%d endpoint=%s", eventCount, deliveryCount, created.Endpoint.ID)
	}
}

func TestClaimWithoutCompleteSnapshotDoesNotMutateDelivery(t *testing.T) {
	apiURL := os.Getenv("WDE_TEST_API_DATABASE_URL")
	adminURL := os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	workerURL := os.Getenv("WDE_TEST_WORKER_DATABASE_URL")
	if apiURL == "" || adminURL == "" || workerURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	api := mustPool(t, ctx, apiURL)
	defer api.Close()
	admin := mustPool(t, ctx, adminURL)
	defer admin.Close()
	worker := mustPool(t, ctx, workerURL)
	defer worker.Close()
	workspaceID, endpointID, eventID, deliveryID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'incomplete-snapshot')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.endpoints(id,workspace_id,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version) VALUES($1,$2,'http','127.0.0.1',8081,1,$3,$4,1)`, endpointID, workspaceID, make([]byte, 16), make([]byte, 12)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.events(id,workspace_id,idempotency_key_hash,idempotency_fingerprint,fingerprint_version,event_type,payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,payload_size,payload_expires_at) VALUES($1,$2,$3,$4,1,'test',1,$5,$6,1,2,$7)`, eventID, workspaceID, make([]byte, 32), bytes.Repeat([]byte{1}, 32), make([]byte, 16), make([]byte, 12), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.deliveries(id,workspace_id,event_id,endpoint_id,next_attempt_at) VALUES($1,$2,$3,$4,'2000-01-01T00:00:00Z')`, deliveryID, workspaceID, eventID, endpointID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := delivery.NewPostgresStore(worker).Claim(ctx, uuid.New(), uuid.New(), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	checkTx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTx.Rollback(ctx)
	if _, err = checkTx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err = checkTx.QueryRow(ctx, `SELECT status,attempt_sequence FROM wde.deliveries WHERE id=$1`, deliveryID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attempts != 0 {
		t.Fatalf("status=%s attempts=%d", status, attempts)
	}
}

func mustPool(t *testing.T, ctx context.Context, url string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}
