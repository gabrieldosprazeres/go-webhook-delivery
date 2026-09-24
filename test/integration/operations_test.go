package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/tenanttx"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTenantTransactionDoesNotLeakAfterCommitRollbackOrPanic(t *testing.T) {
	apiURL, adminURL := os.Getenv("WDE_TEST_API_DATABASE_URL"), os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	if apiURL == "" || adminURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	admin := mustPool(t, ctx, adminURL)
	defer admin.Close()
	first, second := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'tenant-tx-a'),($2,'tenant-tx-b')`, first, second); err != nil {
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(apiURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 1
	api, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer api.Close()
	assertWorkspaceVisible := func(workspaceID uuid.UUID, returned error) {
		t.Helper()
		err := tenanttx.Within(ctx, api, workspaceID, func(tx pgx.Tx) error {
			var visible []uuid.UUID
			rows, err := tx.Query(ctx, `SELECT id FROM wde.workspaces ORDER BY id`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					return err
				}
				visible = append(visible, id)
			}
			if len(visible) != 1 || visible[0] != workspaceID {
				t.Fatalf("visible=%v want only %s", visible, workspaceID)
			}
			return returned
		})
		if !errors.Is(err, returned) {
			t.Fatalf("Within() error=%v want=%v", err, returned)
		}
		assertNoTenantContext(t, ctx, api)
	}
	assertWorkspaceVisible(first, nil)
	rollback := errors.New("force rollback")
	assertWorkspaceVisible(second, rollback)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was not propagated")
			}
		}()
		_ = tenanttx.Within(ctx, api, first, func(pgx.Tx) error { panic("tenant panic") })
	}()
	assertNoTenantContext(t, ctx, api)
}

func assertNoTenantContext(t *testing.T, ctx context.Context, api *pgxpool.Pool) {
	t.Helper()
	var current *string
	var visible int
	if err := api.QueryRow(ctx, `SELECT NULLIF(current_setting('wde.workspace_id',true),''),
		(SELECT count(*) FROM wde.workspaces)`).Scan(&current, &visible); err != nil {
		t.Fatal(err)
	}
	if current != nil || visible != 0 {
		t.Fatalf("tenant leaked: current=%v visible=%d", current, visible)
	}
}

func TestPersistentQuotaIsAtomicAcrossDimensionsRestartAndExpiry(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	if _, err := super.Exec(ctx, `DELETE FROM wde.rate_limit_buckets`); err != nil {
		t.Fatal(err)
	}
	workspaceID, apiKeyID := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'quota-test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	principal := auth.Principal{WorkspaceID: workspaceID, APIKeyID: apiKeyID}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	policy := ratelimit.Policy{Operation: "query", Global: 2, Workspace: 2, APIKey: 2, Window: time.Minute}
	limiter := ratelimit.New(api, materials.RateLimitPepper)
	if err := limiter.Consume(ctx, principal, uuid.Nil, policy); err != nil {
		t.Fatal(err)
	}
	if err := ratelimit.New(api, materials.RateLimitPepper).Consume(ctx, principal, uuid.Nil, policy); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Consume(ctx, principal, uuid.Nil, policy); !errors.Is(err, ratelimit.ErrExceeded) {
		t.Fatalf("third consume error=%v", err)
	}
	var dimensions, total int
	if err := super.QueryRow(ctx, `SELECT count(DISTINCT dimension_type),sum(count) FROM wde.rate_limit_buckets WHERE operation='query'`).Scan(&dimensions, &total); err != nil {
		t.Fatal(err)
	}
	if dimensions != 3 || total != 6 {
		t.Fatalf("dimensions=%d total=%d", dimensions, total)
	}
	if _, err := super.Exec(ctx, `DELETE FROM wde.rate_limit_buckets`); err != nil {
		t.Fatal(err)
	}
	replayPolicy := ratelimit.Policy{Operation: "replay", Global: 10, Workspace: 10, APIKey: 10, Resource: 1, Window: time.Minute}
	resourceID := uuid.New()
	if err := limiter.Consume(ctx, principal, resourceID, replayPolicy); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Consume(ctx, principal, resourceID, replayPolicy); !errors.Is(err, ratelimit.ErrExceeded) {
		t.Fatalf("per-delivery consume error=%v", err)
	}
	if err := super.QueryRow(ctx, `SELECT count(DISTINCT dimension_type),sum(count)
		FROM wde.rate_limit_buckets WHERE operation='replay'`).Scan(&dimensions, &total); err != nil {
		t.Fatal(err)
	}
	if dimensions != 4 || total != 4 {
		t.Fatalf("replay dimensions=%d total=%d", dimensions, total)
	}
	if _, err := super.Exec(ctx, `DELETE FROM wde.rate_limit_buckets`); err != nil {
		t.Fatal(err)
	}
	short := ratelimit.Policy{Operation: "query", Global: 1, Workspace: 1, APIKey: 1, Window: time.Second}
	if err := limiter.Consume(ctx, principal, uuid.Nil, short); err != nil {
		t.Fatal(err)
	}
	if err := limiter.Consume(ctx, principal, uuid.Nil, short); !errors.Is(err, ratelimit.ErrExceeded) {
		t.Fatalf("same-window consume error=%v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := ratelimit.New(api, materials.RateLimitPepper).Consume(ctx, principal, uuid.Nil, short); err != nil {
		t.Fatalf("consume after expiry=%v", err)
	}
	var definition string
	if err := super.QueryRow(ctx, `SELECT pg_get_functiondef('wde.consume_quota(bytea,text,text,uuid,uuid,uuid,integer,integer)'::regprocedure)`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, "transaction_timestamp()") || strings.Contains(definition, "statement_timestamp()") {
		t.Fatalf("quota clock is not transaction-stable: %s", definition)
	}
}

func TestPersistentQuotaGlobalBucketContentionIsExact(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	if _, err := super.Exec(ctx, `DELETE FROM wde.rate_limit_buckets`); err != nil {
		t.Fatal(err)
	}
	workspaceID, apiKeyID := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'quota-contention')`,
		workspaceID); err != nil {
		t.Fatal(err)
	}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	limiter := ratelimit.New(api, materials.RateLimitPepper)
	principal := auth.Principal{WorkspaceID: workspaceID, APIKeyID: apiKeyID}
	policy := ratelimit.Policy{Operation: "query", Global: 8, Workspace: 100, APIKey: 100, Window: time.Minute}
	results := make(chan error, 32)
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- limiter.Consume(ctx, principal, uuid.Nil, policy)
		}()
	}
	group.Wait()
	close(results)
	allowed, denied := 0, 0
	for err := range results {
		if err == nil {
			allowed++
		} else if errors.Is(err, ratelimit.ErrExceeded) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if allowed != 8 || denied != 24 {
		t.Fatalf("allowed=%d denied=%d", allowed, denied)
	}
	var globalCount int
	if err := super.QueryRow(ctx, `SELECT count FROM wde.rate_limit_buckets
		WHERE operation='query' AND dimension_type='global'`).Scan(&globalCount); err != nil {
		t.Fatal(err)
	}
	if globalCount != 8 {
		t.Fatalf("global bucket count=%d", globalCount)
	}
}

func TestFanoutAndPaginationLimitsAreEnforced(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	fanoutWorkspace, listWorkspace := uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'fanout-limit'),($2,'page-limit')`,
		fanoutWorkspace, listWorkspace); err != nil {
		t.Fatal(err)
	}
	for range 101 {
		endpointID := uuid.New()
		if _, err := super.Exec(ctx, `INSERT INTO wde.endpoints
			(id,workspace_id,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version)
			VALUES($1,$2,'http','127.0.0.1',8081,1,$3,$4,1)`,
			endpointID, fanoutWorkspace, make([]byte, 16), make([]byte, 12)); err != nil {
			t.Fatal(err)
		}
		if _, err := super.Exec(ctx, `INSERT INTO wde.endpoint_subscriptions(workspace_id,endpoint_id,event_type)
			VALUES($1,$2,'fanout.test')`, fanoutWorkspace, endpointID); err != nil {
			t.Fatal(err)
		}
	}
	service := event.NewService(event.NewPostgresStore(api), materials)
	if _, err := service.Publish(ctx, fanoutWorkspace, "fanout-overflow",
		[]byte(`{"type":"fanout.test","data":{"safe":true}}`)); !errors.Is(err, event.ErrInvalid) {
		t.Fatalf("fanout publish error=%v", err)
	}
	var events, deliveries int
	if err := super.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wde.events WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.deliveries WHERE workspace_id=$1)`, fanoutWorkspace).
		Scan(&events, &deliveries); err != nil {
		t.Fatal(err)
	}
	if events != 0 || deliveries != 0 {
		t.Fatalf("partial fanout persisted events=%d deliveries=%d", events, deliveries)
	}
	endpointID := uuid.New()
	if _, err := super.Exec(ctx, `INSERT INTO wde.endpoints
		(id,workspace_id,scheme,host_ascii,port,target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version)
		VALUES($1,$2,'http','127.0.0.1',8081,1,$3,$4,1)`, endpointID, listWorkspace,
		make([]byte, 16), make([]byte, 12)); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `WITH inserted AS (
		INSERT INTO wde.events
			(id,workspace_id,idempotency_key_hash,idempotency_fingerprint,fingerprint_version,event_type,
			 payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,payload_size,payload_expires_at)
		SELECT gen_random_uuid(),$1,decode(lpad(to_hex(g),64,'0'),'hex'),decode(repeat('01',32),'hex'),
			1,'page.test',1,decode(repeat('01',16),'hex'),decode(repeat('01',12),'hex'),1,2,
			clock_timestamp()+interval '1 day' FROM generate_series(1,101) g RETURNING id,workspace_id
	)
	INSERT INTO wde.deliveries(id,workspace_id,event_id,endpoint_id)
	SELECT gen_random_uuid(),workspace_id,id,$2 FROM inserted`, listWorkspace, endpointID); err != nil {
		t.Fatal(err)
	}
	store := delivery.NewPostgresStore(api, materials.CursorPepper)
	first, err := store.List(ctx, listWorkspace, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.List(ctx, listWorkspace, 100, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 100 || first.NextCursor == "" || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("first=%d cursor=%t second=%d second_cursor=%t", len(first.Items),
			first.NextCursor != "", len(second.Items), second.NextCursor != "")
	}
	if _, err := store.List(ctx, listWorkspace, 101, ""); !errors.Is(err, delivery.ErrInvalidList) {
		t.Fatalf("limit 101 error=%v", err)
	}
	if _, err := store.List(ctx, listWorkspace, 50, "not-a-cursor"); !errors.Is(err, delivery.ErrInvalidList) {
		t.Fatalf("invalid cursor error=%v", err)
	}
	if _, err := store.List(ctx, fanoutWorkspace, 50, first.NextCursor); !errors.Is(err, delivery.ErrInvalidList) {
		t.Fatalf("cross-tenant cursor error=%v", err)
	}
	tampered := []byte(first.NextCursor)
	tampered[len(tampered)-1] ^= 1
	if _, err := store.List(ctx, listWorkspace, 50, string(tampered)); !errors.Is(err, delivery.ErrInvalidList) {
		t.Fatalf("tampered cursor error=%v", err)
	}
}

func TestReplayGenerationIsConcurrentIdempotentAndPreservesHistory(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "replay-history", 1)
	workerID := uuid.New()
	claim := claimExactlyOne(t, ctx, delivery.NewPostgresStore(worker), workerID)
	if changed, err := delivery.NewPostgresStore(worker).Finalize(ctx, claim, workerID,
		delivery.Result{Disposition: delivery.DispositionPermanent, Category: "operator_test"}); err != nil || !changed {
		t.Fatalf("finalize changed=%v err=%v", changed, err)
	}
	service := delivery.NewReplayService(delivery.NewPostgresStore(api), materials)
	actorID := uuid.New()
	results := make(chan delivery.ReplayResult, 2)
	errorsCh := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.Request(ctx, seeded.workspaceID, seeded.deliveryIDs[0], actorID,
				"replay-same", "operational retry", "req_replay_same")
			if err != nil {
				errorsCh <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	var expected uuid.UUID
	duplicates := 0
	for result := range results {
		if expected == uuid.Nil {
			expected = result.CommandID
		}
		if result.CommandID != expected || result.RunNumber != 2 {
			t.Fatalf("result=%+v expected command=%s run=2", result, expected)
		}
		if result.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("duplicates=%d", duplicates)
	}
	secondClaim := claimExactlyOne(t, ctx, delivery.NewPostgresStore(worker), uuid.New())
	if secondClaim.DeliveryID != seeded.deliveryIDs[0] || secondClaim.AttemptNumber != 1 {
		t.Fatalf("second claim=%+v", secondClaim)
	}
	assertReplayHistory(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"), seeded.deliveryIDs[0])
}

func assertReplayHistory(t *testing.T, ctx context.Context, superURL string, deliveryID uuid.UUID) {
	t.Helper()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	rows, err := super.Query(ctx, `SELECT run_number,attempt_number_in_run,attempt_sequence
		FROM wde.delivery_attempts WHERE delivery_id=$1 ORDER BY attempt_sequence`, deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := [][3]int{{1, 1, 1}, {2, 1, 2}}
	index := 0
	for rows.Next() {
		var got [3]int
		if err := rows.Scan(&got[0], &got[1], &got[2]); err != nil {
			t.Fatal(err)
		}
		if index >= len(want) || got != want[index] {
			t.Fatalf("attempt[%d]=%v want=%v", index, got, want[index])
		}
		index++
	}
	if index != len(want) {
		t.Fatalf("attempt count=%d", index)
	}
	var commands, audits int
	if err := super.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wde.replay_commands WHERE delivery_id=$1),
		(SELECT count(*) FROM wde.audit_events WHERE action='delivery.replay' AND resource_id=$1::text)`,
		deliveryID).Scan(&commands, &audits); err != nil {
		t.Fatal(err)
	}
	if commands != 1 || audits != 1 {
		t.Fatalf("commands=%d audits=%d", commands, audits)
	}
}

func TestReplayConflictAndPurgedPayloadFailClosed(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	first := seedDeliveries(t, ctx, api, admin, materials, "replay-conflict", 1)
	if _, err := super.Exec(ctx, `UPDATE wde.deliveries SET status='failed_permanent',terminal_at=clock_timestamp() WHERE id=$1`, first.deliveryIDs[0]); err != nil {
		t.Fatal(err)
	}
	service := delivery.NewReplayService(delivery.NewPostgresStore(api), materials)
	actorID := uuid.New()
	conflictErrors := make(chan error, 2)
	var conflictGroup sync.WaitGroup
	for index, reason := range []string{"first reason", "different reason"} {
		conflictGroup.Add(1)
		go func() {
			defer conflictGroup.Done()
			_, err := service.Request(ctx, first.workspaceID, first.deliveryIDs[0], actorID,
				"replay-conflict-key", reason, "req_conflict_"+string(rune('a'+index)))
			conflictErrors <- err
		}()
	}
	conflictGroup.Wait()
	close(conflictErrors)
	succeeded, conflicted := 0, 0
	for err := range conflictErrors {
		if err == nil {
			succeeded++
		} else if errors.Is(err, delivery.ErrReplayConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	second := seedDeliveries(t, ctx, api, admin, materials, "replay-purged", 1)
	if _, err := super.Exec(ctx, `UPDATE wde.deliveries SET status='failed_permanent',terminal_at=clock_timestamp() WHERE id=$1`, second.deliveryIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `UPDATE wde.events SET payload_ciphertext=NULL,payload_nonce=NULL,
		payload_kek_version=NULL,payload_cipher_format_version=NULL,payload_purged_at=clock_timestamp()
		WHERE id=(SELECT event_id FROM wde.deliveries WHERE id=$1)`, second.deliveryIDs[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Request(ctx, second.workspaceID, second.deliveryIDs[0], actorID,
		"replay-purged-key", "operator retry", "req_purged"); !errors.Is(err, delivery.ErrPayloadPurged) {
		t.Fatalf("purged replay error=%v", err)
	}
	var status string
	var run, commands int
	if err := super.QueryRow(ctx, `SELECT status,run_number,
		(SELECT count(*) FROM wde.replay_commands WHERE delivery_id=$1)
		FROM wde.deliveries WHERE id=$1`, second.deliveryIDs[0]).Scan(&status, &run, &commands); err != nil {
		t.Fatal(err)
	}
	if status != "failed_permanent" || run != 1 || commands != 0 {
		t.Fatalf("status=%s run=%d commands=%d", status, run, commands)
	}
}

func TestReplayHTTPContractScopeAndTenantIsolation(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "replay-http", 1)
	if _, err := super.Exec(ctx, `UPDATE wde.deliveries
		SET status='failed_permanent',terminal_at=clock_timestamp() WHERE id=$1`, seeded.deliveryIDs[0]); err != nil {
		t.Fatal(err)
	}
	token := insertReplayCredential(t, ctx, admin, materials, seeded.workspaceID, true)
	limitedToken := insertReplayCredential(t, ctx, admin, materials, seeded.workspaceID, false)
	otherWorkspaceID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'replay-http-other')`,
		otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	otherToken := insertReplayCredential(t, ctx, admin, materials, otherWorkspaceID, true)

	store := delivery.NewPostgresStore(api)
	handler := delivery.NewHandler(store, delivery.NewReplayService(store, materials))
	authenticator := auth.NewAuthenticator(auth.NewPostgresLookup(api), materials.AuthPepper)
	mux := http.NewServeMux()
	mux.Handle("POST /v1/deliveries/{id}/replays",
		authenticator.Middleware("deliveries:retry", http.HandlerFunc(handler.Replay)))
	server := httptest.NewServer(problem.WithRequestID(problem.Recover(mux)))
	defer server.Close()
	url := server.URL + "/v1/deliveries/" + seeded.deliveryIDs[0].String() + "/replays"

	assertProblem(t, authorizedJSON(t, http.MethodPost, url, limitedToken, "limited", `{"reason":"retry"}`),
		http.StatusForbidden, "insufficient_scope")
	assertProblem(t, authorizedJSON(t, http.MethodPost, url, token, "", `{"reason":"retry"}`),
		http.StatusBadRequest, "idempotency_key_required")
	assertProblem(t, authorizedJSON(t, http.MethodPost, url, otherToken, "other-tenant", `{"reason":"retry"}`),
		http.StatusNotFound, "not_found")

	accepted := authorizedJSON(t, http.MethodPost, url, token, "http-replay-key", `{"reason":"operator retry"}`)
	defer accepted.Body.Close()
	if accepted.StatusCode != http.StatusAccepted {
		t.Fatalf("accepted status=%d", accepted.StatusCode)
	}
	var first delivery.ReplayResult
	if err := json.NewDecoder(accepted.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	duplicate := authorizedJSON(t, http.MethodPost, url, token, "http-replay-key", `{"reason":"operator retry"}`)
	defer duplicate.Body.Close()
	var second delivery.ReplayResult
	if duplicate.StatusCode != http.StatusAccepted || json.NewDecoder(duplicate.Body).Decode(&second) != nil ||
		second.CommandID != first.CommandID || !second.Duplicate {
		t.Fatalf("duplicate status=%d first=%+v second=%+v", duplicate.StatusCode, first, second)
	}
	assertProblem(t, authorizedJSON(t, http.MethodPost, url, token, "http-replay-key", `{"reason":"changed"}`),
		http.StatusConflict, "idempotency_conflict")
}

func insertReplayCredential(t *testing.T, ctx context.Context, admin *pgxpool.Pool,
	materials cryptobox.Materials, workspaceID uuid.UUID, retryScope bool) string {
	t.Helper()
	token, prefix, verifier, err := auth.Generate(true, materials.AuthPepper)
	if err != nil {
		t.Fatal(err)
	}
	keyID := uuid.New()
	if _, err = admin.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier)
		VALUES($1,$2,$3,$4)`, keyID, workspaceID, prefix, verifier[:]); err != nil {
		t.Fatal(err)
	}
	if retryScope {
		if _, err = admin.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope)
			VALUES($1,$2,'deliveries:retry')`, workspaceID, keyID); err != nil {
			t.Fatal(err)
		}
	} else if _, err = admin.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope)
		VALUES($1,$2,'deliveries:read')`, workspaceID, keyID); err != nil {
		t.Fatal(err)
	}
	return token
}

func TestOperationsACLAndAuditSnapshotsAreImmutable(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	assertOperationsFunctionSecurity(t, ctx, super)
	for _, pool := range []*pgxpool.Pool{api, admin, worker} {
		if _, err := pool.Exec(ctx, `UPDATE wde.audit_events SET outcome='revoked'`); err == nil {
			t.Fatal("runtime role updated audit events")
		}
		if _, err := pool.Exec(ctx, `DELETE FROM wde.audit_events`); err == nil {
			t.Fatal("runtime role deleted audit events")
		}
	}
	workspaceID, keyID, auditID := uuid.New(), uuid.New(), uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'audit-snapshot')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier)
		VALUES($1,$2,'abcdef0123456789',$3)`, keyID, workspaceID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `SELECT wde.append_audit_event($1,$2,'admin_cli','snapshot-test',
		'credential.revoke','api_key',$3,'req_snapshot','revoked',NULL)`, auditID, workspaceID, keyID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `DELETE FROM wde.api_keys WHERE id=$1`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `DELETE FROM wde.workspaces WHERE id=$1`, workspaceID); err != nil {
		t.Fatal(err)
	}
	var actorID, resourceID string
	if err := super.QueryRow(ctx, `SELECT actor_id,resource_id FROM wde.audit_events WHERE id=$1`, auditID).Scan(&actorID, &resourceID); err != nil {
		t.Fatal(err)
	}
	if actorID != "snapshot-test" || resourceID != keyID.String() {
		t.Fatalf("actor=%q resource=%q", actorID, resourceID)
	}
	canaryWorkspace := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'audit-canary')`, canaryWorkspace); err != nil {
		t.Fatal(err)
	}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	created, err := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials).
		CreateAs(ctx, canaryWorkspace, "api_key", uuid.NewString(), "req_audit_canary",
			endpoint.CreateInput{URL: "http://127.0.0.1:8081/private-canary-path", EventTypes: []string{"audit.canary"}})
	if err != nil {
		t.Fatal(err)
	}
	var auditText string
	if err := super.QueryRow(ctx, `SELECT COALESCE(string_agg(row_to_json(a)::text,''),'')
		FROM wde.audit_events a WHERE workspace_id=$1`, canaryWorkspace).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-canary-path", created.SigningSecret.Secret, "ciphertext", "payload"} {
		if strings.Contains(auditText, forbidden) {
			t.Fatalf("audit contains forbidden canary %q: %s", forbidden, auditText)
		}
	}
}

func assertOperationsFunctionSecurity(t *testing.T, ctx context.Context, super *pgxpool.Pool) {
	t.Helper()
	functions := []struct{ signature, owner string }{
		{auditV4, "wde_audit_executor"}, {quotaV4, "wde_quota_executor"}, {replayV4, "wde_replay_executor"},
	}
	for _, function := range functions {
		var owner, definition string
		var securityDefiner, publicExecute bool
		var settings []string
		if err := super.QueryRow(ctx, `SELECT pg_get_userbyid(proowner),prosecdef,
			COALESCE(proconfig,ARRAY[]::text[]),has_function_privilege('public',oid,'EXECUTE'),
			pg_get_functiondef(oid) FROM pg_proc WHERE oid=to_regprocedure($1)`, function.signature).
			Scan(&owner, &securityDefiner, &settings, &publicExecute, &definition); err != nil {
			t.Fatal(err)
		}
		if owner != function.owner || !securityDefiner || publicExecute || len(settings) != 1 ||
			settings[0] != "search_path=pg_catalog" || strings.Contains(strings.ToUpper(definition), "EXECUTE ") {
			t.Fatalf("function=%s owner=%s definer=%v settings=%v public=%v", function.signature,
				owner, securityDefiner, settings, publicExecute)
		}
	}
	for _, table := range []string{"audit_events", "replay_commands", "rate_limit_buckets"} {
		var protected bool
		if err := super.QueryRow(ctx, `SELECT relrowsecurity AND relforcerowsecurity
			FROM pg_class WHERE oid=to_regclass($1)`, "wde."+table).Scan(&protected); err != nil {
			t.Fatal(err)
		}
		if !protected {
			t.Fatalf("table %s is not FORCE RLS", table)
		}
	}
	var hardenedRoles int
	if err := super.QueryRow(ctx, `SELECT count(*) FROM pg_roles
		WHERE rolname IN ('wde_quota_executor','wde_replay_executor')
		AND NOT rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole
		AND NOT rolreplication AND NOT rolbypassrls`).Scan(&hardenedRoles); err != nil {
		t.Fatal(err)
	}
	if hardenedRoles != 2 {
		t.Fatalf("hardened executor roles=%d", hardenedRoles)
	}
	var quotaCreate, replayCreate, apiMember, workerMember, adminMember bool
	if err := super.QueryRow(ctx, `SELECT
		has_schema_privilege('wde_quota_executor','wde','CREATE'),
		has_schema_privilege('wde_replay_executor','wde','CREATE'),
		pg_has_role('wde_api','wde_quota_executor','MEMBER'),
		pg_has_role('wde_worker','wde_replay_executor','MEMBER'),
		pg_has_role('wde_admin','wde_replay_executor','MEMBER')`).Scan(
		&quotaCreate, &replayCreate, &apiMember, &workerMember, &adminMember); err != nil {
		t.Fatal(err)
	}
	if quotaCreate || replayCreate || apiMember || workerMember || adminMember {
		t.Fatalf("executor escalation create=%v/%v membership=%v/%v/%v",
			quotaCreate, replayCreate, apiMember, workerMember, adminMember)
	}
	var authRate, quotaKeys, replayAudit, auditDeliveries bool
	if err := super.QueryRow(ctx, `SELECT
		has_table_privilege('wde_auth_executor','wde.rate_limit_buckets','SELECT'),
		has_table_privilege('wde_quota_executor','wde.api_keys','SELECT'),
		has_table_privilege('wde_replay_executor','wde.audit_events','INSERT'),
		has_table_privilege('wde_audit_executor','wde.deliveries','SELECT')`).Scan(
		&authRate, &quotaKeys, &replayAudit, &auditDeliveries); err != nil {
		t.Fatal(err)
	}
	if authRate || quotaKeys || replayAudit || auditDeliveries {
		t.Fatalf("cross-capability privileges auth-rate=%v quota-keys=%v replay-audit=%v audit-deliveries=%v",
			authRate, quotaKeys, replayAudit, auditDeliveries)
	}
}
