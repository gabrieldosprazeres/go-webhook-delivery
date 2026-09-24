package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBatchClaimFairnessAndConcurrentWorkers(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	saturated := seedDeliveries(t, ctx, api, admin, materials, "saturated", 8)
	quiet := seedDeliveries(t, ctx, api, admin, materials, "quiet", 2)
	store := delivery.NewPostgresStore(worker)

	fairWorkerID := uuid.New()
	first, err := store.ClaimBatch(ctx, delivery.ClaimRequest{
		WorkerID: fairWorkerID, LeaseTTL: 30 * time.Second, Limit: 6, WorkspaceLimit: 2, EndpointLimit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFairBatch(t, first, saturated.workspaceID, quiet.workspaceID)
	finalizeClaims(t, ctx, store, fairWorkerID, first)
	seedDeliveries(t, ctx, api, admin, materials, "concurrent-a", 2)
	seedDeliveries(t, ctx, api, admin, materials, "concurrent-b", 2)

	type claimed struct {
		worker uuid.UUID
		claim  delivery.Claim
	}
	results := make(chan claimed, 8)
	var group sync.WaitGroup
	for range 4 {
		group.Add(1)
		go func() {
			defer group.Done()
			workerID := uuid.New()
			claims, claimErr := store.ClaimBatch(ctx, delivery.ClaimRequest{
				WorkerID: workerID, LeaseTTL: 30 * time.Second, Limit: 2, WorkspaceLimit: 2, EndpointLimit: 2,
			})
			if claimErr != nil {
				t.Errorf("claim: %v", claimErr)
				return
			}
			for _, claim := range claims {
				results <- claimed{worker: workerID, claim: claim}
			}
		}()
	}
	group.Wait()
	close(results)
	seen := map[uuid.UUID]uuid.UUID{}
	workersWithClaims := map[uuid.UUID]struct{}{}
	for item := range results {
		if previous, duplicate := seen[item.claim.DeliveryID]; duplicate {
			t.Fatalf("delivery %s claimed by %s and %s", item.claim.DeliveryID, previous, item.worker)
		}
		seen[item.claim.DeliveryID] = item.worker
		workersWithClaims[item.worker] = struct{}{}
	}
	// A worker may observe every eligible workspace temporarily locked by the
	// other claim transactions. SKIP LOCKED must return promptly in that case;
	// the scheduler's following poll performs the next fair cycle.
	if len(seen) < 6 || len(seen) > 8 || len(workersWithClaims) < 3 {
		t.Fatalf("claimed=%d workers=%d", len(seen), len(workersWithClaims))
	}
}

func TestLockedWorkspaceDoesNotBlockIndependentClaim(t *testing.T) {
	superURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL")
	if superURL == "" {
		t.Skip("superuser integration database URL is not configured")
	}
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	locked := seedDeliveries(t, ctx, api, admin, materials, "locked-workspace", 4)
	independent := seedDeliveries(t, ctx, api, admin, materials, "independent-workspace", 1)
	tx, err := super.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM wde.workspaces WHERE id=$1 FOR UPDATE`, locked.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT endpoint_id FROM wde.endpoint_runtime WHERE workspace_id=$1 FOR UPDATE`, locked.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT id FROM wde.deliveries WHERE workspace_id=$1 FOR UPDATE`, locked.workspaceID); err != nil {
		t.Fatal(err)
	}
	store := delivery.NewPostgresStore(worker)
	workerID := uuid.New()
	started := time.Now()
	claim := claimExactlyOne(t, ctx, store, workerID)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("independent claim blocked for %s", elapsed)
	}
	if claim.WorkspaceID != independent.workspaceID {
		t.Fatalf("claimed workspace=%s want=%s", claim.WorkspaceID, independent.workspaceID)
	}
	finalizeClaims(t, ctx, store, workerID, []delivery.Claim{claim})
}

func TestClaimPlanUsesReadyIndexAtRepresentativeScale(t *testing.T) {
	superURL, apiURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"), os.Getenv("WDE_TEST_API_DATABASE_URL")
	adminURL, workerURL := os.Getenv("WDE_TEST_ADMIN_DATABASE_URL"), os.Getenv("WDE_TEST_WORKER_DATABASE_URL")
	if superURL == "" || apiURL == "" || adminURL == "" || workerURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	control := mustPool(t, ctx, superURL)
	defer control.Close()
	databaseName := "wde_claim_plan_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{databaseName}.Sanitize()
	if _, err := control.Exec(ctx, "CREATE DATABASE "+identifier+" OWNER wde_owner"); err != nil {
		t.Fatal(err)
	}
	defer dropBoundaryDatabase(t, ctx, control, databaseName, identifier)
	if _, err := control.Exec(ctx, "REVOKE ALL ON DATABASE "+identifier+
		" FROM PUBLIC; GRANT CONNECT ON DATABASE "+identifier+" TO wde_migrator,wde_api,wde_worker,wde_admin"); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	isolatedSuperURL := databaseURL(t, superURL, databaseName)
	runGooseBoundary(t, ctx, root, isolatedSuperURL, "up-to", 10)
	api := mustPool(t, ctx, databaseURL(t, apiURL, databaseName))
	admin := mustPool(t, ctx, databaseURL(t, adminURL, databaseName))
	worker := mustPool(t, ctx, databaseURL(t, workerURL, databaseName))
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, isolatedSuperURL)
	defer super.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "claim-plan", 1)
	if _, err := super.Exec(ctx, `WITH generated AS (
		INSERT INTO wde.events(id,workspace_id,idempotency_key_hash,idempotency_fingerprint,
			fingerprint_version,event_type,payload_cipher_format_version,payload_ciphertext,
			payload_nonce,payload_kek_version,payload_size,payload_expires_at)
		SELECT gen_random_uuid(),$1,decode(md5(gs::text)||md5('key'||gs::text),'hex'),
			decode(md5('finger'||gs::text)||md5('print'||gs::text),'hex'),1,'reliability.test',
			1,decode(repeat('01',16),'hex'),decode(repeat('02',12),'hex'),1,2,clock_timestamp()+interval '1 hour'
		FROM generate_series(1,5000) gs RETURNING id,workspace_id)
		INSERT INTO wde.deliveries(id,workspace_id,event_id,endpoint_id,next_attempt_at)
		SELECT gen_random_uuid(),g.workspace_id,g.id,d.endpoint_id,clock_timestamp()+interval '24 hours'
		FROM generated g CROSS JOIN (SELECT endpoint_id FROM wde.deliveries WHERE id=$2) d`,
		seeded.workspaceID, seeded.deliveryIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `ANALYZE wde.deliveries`); err != nil {
		t.Fatal(err)
	}
	rows, err := super.Query(ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT TEXT)
		WITH eligible AS MATERIALIZED (
			SELECT d.id FROM wde.deliveries d
			 WHERE d.status IN ('pending','retry_scheduled') AND d.next_attempt_at <= statement_timestamp()
			   AND d.attempts_in_run < d.max_attempts_per_run
			UNION ALL
			SELECT d.id FROM wde.deliveries d
			 WHERE d.status='processing' AND d.lease_expires_at <= statement_timestamp()
			   AND d.attempts_in_run < d.max_attempts_per_run)
		SELECT d.id FROM eligible ready JOIN wde.deliveries d ON d.id=ready.id
		WHERE d.attempts_in_run < d.max_attempts_per_run
		  AND ((d.status IN ('pending','retry_scheduled') AND d.next_attempt_at <= statement_timestamp())
		    OR (d.status='processing' AND d.lease_expires_at <= statement_timestamp()))
		ORDER BY d.next_attempt_at,d.created_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT 8`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(line)
		plan.WriteByte('\n')
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if output := plan.String(); !strings.Contains(output, "deliveries_ready_idx") ||
		strings.Contains(output, "Seq Scan on deliveries") {
		t.Fatalf("claim plan did not use bounded ready index:\n%s", output)
	}
}

func TestPersistentFairnessAcrossSingleSlotCycles(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	saturated := seedDeliveries(t, ctx, api, admin, materials, "slot-saturated", 8)
	quiet := seedDeliveries(t, ctx, api, admin, materials, "slot-quiet", 4)
	store := delivery.NewPostgresStore(worker)
	counts := map[uuid.UUID]int{}
	var previous uuid.UUID
	for cycle := range 8 {
		workerID := uuid.New()
		claim := claimExactlyOne(t, ctx, store, workerID)
		counts[claim.WorkspaceID]++
		if cycle > 0 && claim.WorkspaceID == previous {
			t.Fatalf("workspace repeated before quiet tenant drained: cycle=%d workspace=%s", cycle, claim.WorkspaceID)
		}
		previous = claim.WorkspaceID
		finalizeClaims(t, ctx, store, workerID, []delivery.Claim{claim})
	}
	if counts[saturated.workspaceID] != 4 || counts[quiet.workspaceID] != 4 {
		t.Fatalf("persistent fairness counts=%v", counts)
	}
}

func TestPersistentFairnessWithConcurrentSingleSlotWorkers(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	first := seedDeliveries(t, ctx, api, admin, materials, "concurrent-slot-a", 30)
	second := seedDeliveries(t, ctx, api, admin, materials, "concurrent-slot-b", 30)
	store := delivery.NewPostgresStore(worker)
	type claimed struct {
		workerID uuid.UUID
		claim    delivery.Claim
		err      error
	}
	counts := map[uuid.UUID]int{}
	seen := map[uuid.UUID]struct{}{}
	for cycle := range 10 {
		results := make(chan claimed, 4)
		var group sync.WaitGroup
		for range 4 {
			group.Add(1)
			go func() {
				defer group.Done()
				workerID := uuid.New()
				claims, err := store.ClaimBatch(ctx, delivery.ClaimRequest{
					WorkerID: workerID, LeaseTTL: 20 * time.Second, Limit: 1, WorkspaceLimit: 1, EndpointLimit: 1,
				})
				if err != nil || len(claims) != 1 {
					results <- claimed{err: fmt.Errorf("claims=%d: %w", len(claims), err)}
					return
				}
				results <- claimed{workerID: workerID, claim: claims[0]}
			}()
		}
		group.Wait()
		close(results)
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if _, duplicate := seen[result.claim.DeliveryID]; duplicate {
				t.Fatalf("cycle=%d duplicate delivery=%s", cycle, result.claim.DeliveryID)
			}
			seen[result.claim.DeliveryID] = struct{}{}
			counts[result.claim.WorkspaceID]++
			finalizeClaims(t, ctx, store, result.workerID, []delivery.Claim{result.claim})
		}
	}
	difference := counts[first.workspaceID] - counts[second.workspaceID]
	if difference < 0 {
		difference = -difference
	}
	if counts[first.workspaceID] < 16 || counts[second.workspaceID] < 16 || difference > 8 {
		t.Fatalf("concurrent persistent fairness counts=%v", counts)
	}
}

func TestEndpointCapacityDoesNotStarveHealthyEndpoint(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	workspaceID, slowEndpoint := seedEndpointPair(t, ctx, api, admin, materials, 8)
	store := delivery.NewPostgresStore(worker)
	slow, healthy := 0, 0
	for range 10 {
		workerID := uuid.New()
		claim := claimExactlyOne(t, ctx, store, workerID)
		if claim.WorkspaceID != workspaceID {
			t.Fatalf("unexpected workspace %s", claim.WorkspaceID)
		}
		if claim.EndpointID == slowEndpoint {
			slow++
			continue
		}
		healthy++
		finalizeClaims(t, ctx, store, workerID, []delivery.Claim{claim})
	}
	if slow != 4 || healthy < 6 {
		t.Fatalf("slow=%d healthy=%d", slow, healthy)
	}
}

func TestLeaseRecoveryFencingAndAbandonedAttempt(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "crash", 1)
	store := delivery.NewPostgresStore(worker)
	workerOne, workerTwo := uuid.New(), uuid.New()
	first := claimExactlyOne(t, ctx, store, workerOne)
	expiresAt := leaseExpiry(t, ctx, api, seeded.workspaceID, first.DeliveryID)
	if wait := time.Until(expiresAt.Add(100 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}
	recoveryStarted := time.Now()
	second := claimExactlyOne(t, ctx, store, workerTwo)
	if time.Since(recoveryStarted) > 5*time.Second {
		t.Fatal("lease recovery exceeded TTL + 5s")
	}
	if second.DeliveryID != first.DeliveryID || second.FencingToken <= first.FencingToken || second.AttemptNumber != 2 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	changed, err := store.Finalize(ctx, first, workerOne, delivery.Result{Disposition: delivery.DispositionSuccess})
	if err != nil || changed {
		t.Fatalf("stale finalize changed=%v err=%v", changed, err)
	}
	changed, err = store.Finalize(ctx, second, workerTwo, delivery.Result{Disposition: delivery.DispositionSuccess})
	if err != nil || !changed {
		t.Fatalf("current finalize changed=%v err=%v", changed, err)
	}
	details, err := delivery.NewPostgresStore(api).Get(ctx, seeded.workspaceID, first.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if len(details.Attempts) != 2 || details.Attempts[0].State != "abandoned" ||
		details.Attempts[0].Outcome == nil || *details.Attempts[0].Outcome != "stale" ||
		details.Attempts[1].State != "completed" {
		t.Fatalf("attempts=%+v", details.Attempts)
	}
}

func TestRetryHistoryDeadLetterAndNeverMaxPlusOne(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "dead-letter", 1)
	store := delivery.NewPostgresStore(worker)
	workerID := uuid.New()
	for attempt := 1; attempt <= 10; attempt++ {
		claim := claimExactlyOne(t, ctx, store, workerID)
		if int(claim.AttemptNumber) != attempt || claim.MaxAttempts != 10 {
			t.Fatalf("attempt=%d claim=%+v", attempt, claim)
		}
		changed, err := store.Finalize(ctx, claim, workerID, delivery.Result{
			Disposition: delivery.DispositionRetry, Category: "network", RetryAfter: 0,
		})
		if err != nil || !changed {
			t.Fatalf("attempt=%d changed=%v err=%v", attempt, changed, err)
		}
	}
	claims, err := store.ClaimBatch(ctx, delivery.ClaimRequest{
		WorkerID: workerID, LeaseTTL: 30 * time.Second, Limit: 1, WorkspaceLimit: 1, EndpointLimit: 1,
	})
	if err != nil || len(claims) != 0 {
		t.Fatalf("extra claims=%d err=%v", len(claims), err)
	}
	details, err := delivery.NewPostgresStore(api).Get(ctx, seeded.workspaceID, seeded.deliveryIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if details.Status != "dead_letter" || len(details.Attempts) != 10 {
		t.Fatalf("status=%s attempts=%d", details.Status, len(details.Attempts))
	}
	for index, attempt := range details.Attempts {
		if attempt.Sequence != index+1 || attempt.State != "completed" ||
			attempt.Outcome == nil || *attempt.Outcome != "retry" {
			t.Fatalf("attempt[%d]=%+v", index, attempt)
		}
	}
}

func TestRepeatedCrashesStopAtMaximumAttempts(t *testing.T) {
	superURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL")
	if superURL == "" {
		t.Skip("superuser integration database URL is not configured")
	}
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "repeated-crash", 1)
	if _, err := super.Exec(ctx, `UPDATE wde.deliveries SET max_attempts_per_run=3 WHERE id=$1`, seeded.deliveryIDs[0]); err != nil {
		t.Fatal(err)
	}
	store := delivery.NewPostgresStore(worker)
	for attempt := 1; attempt <= 3; attempt++ {
		claim := claimExactlyOne(t, ctx, store, uuid.New())
		if int(claim.AttemptNumber) != attempt {
			t.Fatalf("attempt=%d claim=%+v", attempt, claim)
		}
		if _, err := super.Exec(ctx, `UPDATE wde.deliveries SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.DeliveryID); err != nil {
			t.Fatal(err)
		}
	}
	claims, err := store.ClaimBatch(ctx, delivery.ClaimRequest{
		WorkerID: uuid.New(), LeaseTTL: 20 * time.Second, Limit: 1, WorkspaceLimit: 1, EndpointLimit: 1,
	})
	if err != nil || len(claims) != 0 {
		t.Fatalf("max+1 claims=%d err=%v", len(claims), err)
	}
	details, err := delivery.NewPostgresStore(api).Get(ctx, seeded.workspaceID, seeded.deliveryIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if details.Status != "dead_letter" || len(details.Attempts) != 3 {
		t.Fatalf("status=%s attempts=%d", details.Status, len(details.Attempts))
	}
	for index, attempt := range details.Attempts {
		if attempt.Sequence != index+1 || attempt.State != "abandoned" {
			t.Fatalf("attempt[%d]=%+v", index, attempt)
		}
	}
}

func TestHostileHTTPStatusDoesNotBreakFinalizeOrNextTenant(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	hostile := seedDeliveries(t, ctx, api, admin, materials, "hostile-status", 1)
	store := delivery.NewPostgresStore(worker)
	workerID := uuid.New()
	claim := claimExactlyOne(t, ctx, store, workerID)
	var changed bool
	err := worker.QueryRow(ctx, `SELECT wde.finalize_delivery($1,$2,$3,$4,'retry',699::smallint,1,'http_retryable',interval '1 second')`,
		claim.WorkspaceID, claim.DeliveryID, workerID, claim.FencingToken).Scan(&changed)
	if err != nil || !changed {
		t.Fatalf("hostile finalize changed=%v err=%v", changed, err)
	}
	details, err := delivery.NewPostgresStore(api).Get(ctx, hostile.workspaceID, claim.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if details.Status != "failed_permanent" || len(details.Attempts) != 1 ||
		details.Attempts[0].HTTPStatus != nil || details.Attempts[0].ErrorCategory == nil ||
		*details.Attempts[0].ErrorCategory != "invalid_http_status" {
		t.Fatalf("details=%+v", details)
	}
	healthy := seedDeliveries(t, ctx, api, admin, materials, "after-hostile", 1)
	nextWorker := uuid.New()
	next := claimExactlyOne(t, ctx, store, nextWorker)
	if next.WorkspaceID != healthy.workspaceID {
		t.Fatalf("next tenant=%s want=%s", next.WorkspaceID, healthy.workspaceID)
	}
	finalizeClaims(t, ctx, store, nextWorker, []delivery.Claim{next})
}

type seededDeliveries struct {
	workspaceID uuid.UUID
	deliveryIDs []uuid.UUID
}

func seedDeliveries(t *testing.T, ctx context.Context, api, admin *pgxpool.Pool, materials cryptobox.Materials, name string, count int) seededDeliveries {
	t.Helper()
	workspaceID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,$2)`, workspaceID, name); err != nil {
		t.Fatal(err)
	}
	endpointService := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials)
	if _, err := endpointService.Create(ctx, workspaceID, endpoint.CreateInput{
		URL: "http://127.0.0.1:18081/success", EventTypes: []string{"reliability.test"},
	}); err != nil {
		t.Fatal(err)
	}
	eventService := event.NewService(event.NewPostgresStore(api), materials)
	result := seededDeliveries{workspaceID: workspaceID, deliveryIDs: make([]uuid.UUID, 0, count)}
	for index := range count {
		published, err := eventService.Publish(ctx, workspaceID, fmt.Sprintf("%s-%d", name, index),
			[]byte(fmt.Sprintf(`{"type":"reliability.test","data":{"index":%d}}`, index)))
		if err != nil {
			t.Fatal(err)
		}
		result.deliveryIDs = append(result.deliveryIDs, published.DeliveryIDs...)
	}
	waitForStableEligibility(t, ctx, api, workspaceID)
	return result
}

func seedEndpointPair(t *testing.T, ctx context.Context, api, admin *pgxpool.Pool, materials cryptobox.Materials, count int) (uuid.UUID, uuid.UUID) {
	t.Helper()
	workspaceID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'endpoint-fairness')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	endpointService := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials)
	slow, err := endpointService.Create(ctx, workspaceID, endpoint.CreateInput{
		URL: "http://127.0.0.1:18081/timeout", EventTypes: []string{"endpoint.fairness"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = endpointService.Create(ctx, workspaceID, endpoint.CreateInput{
		URL: "http://127.0.0.1:18081/success", EventTypes: []string{"endpoint.fairness"},
	}); err != nil {
		t.Fatal(err)
	}
	eventService := event.NewService(event.NewPostgresStore(api), materials)
	for index := range count {
		if _, err = eventService.Publish(ctx, workspaceID, fmt.Sprintf("endpoint-fairness-%d", index),
			[]byte(fmt.Sprintf(`{"type":"endpoint.fairness","data":{"index":%d}}`, index))); err != nil {
			t.Fatal(err)
		}
	}
	waitForStableEligibility(t, ctx, api, workspaceID)
	return workspaceID, slow.Endpoint.ID
}

func waitForStableEligibility(t *testing.T, ctx context.Context, api *pgxpool.Pool, workspaceID uuid.UUID) {
	t.Helper()
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var ready bool
		err = tx.QueryRow(ctx, `SELECT COALESCE(bool_and(next_attempt_at <=
			statement_timestamp() - interval '100 milliseconds'),true) FROM wde.deliveries`).Scan(&ready)
		if err != nil {
			t.Fatal(err)
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("new deliveries did not become stably eligible within 2s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func reliabilityPools(t *testing.T) (context.Context, *pgxpool.Pool, *pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	apiURL, adminURL, workerURL := os.Getenv("WDE_TEST_API_DATABASE_URL"), os.Getenv("WDE_TEST_ADMIN_DATABASE_URL"), os.Getenv("WDE_TEST_WORKER_DATABASE_URL")
	if apiURL == "" || adminURL == "" || workerURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	return ctx, mustPool(t, ctx, apiURL), mustPool(t, ctx, adminURL), mustPool(t, ctx, workerURL)
}

func suspendActiveWorkspaces(t *testing.T, ctx context.Context, admin *pgxpool.Pool) {
	t.Helper()
	if _, err := admin.Exec(ctx, `UPDATE wde.workspaces SET status='suspended',updated_at=clock_timestamp() WHERE status='active'`); err != nil {
		t.Fatal(err)
	}
}

func assertFairBatch(t *testing.T, claims []delivery.Claim, saturated, quiet uuid.UUID) {
	t.Helper()
	counts := map[uuid.UUID]int{}
	endpoints := map[uuid.UUID]int{}
	for _, claim := range claims {
		counts[claim.WorkspaceID]++
		endpoints[claim.EndpointID]++
	}
	if len(claims) != 4 || counts[saturated] != 2 || counts[quiet] != 2 {
		t.Fatalf("claims=%d workspace counts=%v", len(claims), counts)
	}
	for endpointID, count := range endpoints {
		if count > 2 {
			t.Fatalf("endpoint %s claimed %d", endpointID, count)
		}
	}
}

func finalizeClaims(t *testing.T, ctx context.Context, store *delivery.PostgresStore, workerID uuid.UUID, claims []delivery.Claim) {
	t.Helper()
	for _, claim := range claims {
		changed, err := store.Finalize(ctx, claim, workerID, delivery.Result{Disposition: delivery.DispositionSuccess})
		if err != nil || !changed {
			t.Fatalf("finalize changed=%v err=%v", changed, err)
		}
	}
}

func claimExactlyOne(t *testing.T, ctx context.Context, store *delivery.PostgresStore, workerID uuid.UUID) delivery.Claim {
	t.Helper()
	claims, err := store.ClaimBatch(ctx, delivery.ClaimRequest{
		WorkerID: workerID, LeaseTTL: 20 * time.Second, Limit: 1, WorkspaceLimit: 1, EndpointLimit: 1,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims=%d err=%v", len(claims), err)
	}
	return claims[0]
}

func leaseExpiry(t *testing.T, ctx context.Context, api *pgxpool.Pool, workspaceID, deliveryID uuid.UUID) time.Time {
	t.Helper()
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	var expiresAt time.Time
	if err = tx.QueryRow(ctx, `SELECT lease_expires_at FROM wde.deliveries WHERE id=$1`, deliveryID).Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	return expiresAt
}
