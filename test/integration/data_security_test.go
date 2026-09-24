package integration

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/database"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/retention"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestSecretRotationIsConcurrentIdempotentAndDualSigns(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	workspaceID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'rotation-s4')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer(nil)
	defer receiver.Close()
	parsed, _ := url.Parse(receiver.URL)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	service := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials)
	created, err := service.Create(ctx, workspaceID, endpoint.CreateInput{
		URL: "http://" + parsed.Host + "/success", EventTypes: []string{"rotation.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	const concurrency = 12
	results := make(chan endpoint.RotationResult, concurrency)
	errorsFound := make(chan error, concurrency)
	var group sync.WaitGroup
	for range concurrency {
		group.Add(1)
		go func() {
			defer group.Done()
			result, rotateErr := service.Rotate(ctx, workspaceID, created.Endpoint.ID, uuid.NewString(),
				"req-rotation", "same-rotation", endpoint.RotationInput{OverlapSeconds: 3600})
			results <- result
			errorsFound <- rotateErr
		}()
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for rotateErr := range errorsFound {
		if rotateErr != nil {
			t.Fatal(rotateErr)
		}
	}
	var keyID, secret string
	newCount, duplicateCount := 0, 0
	for result := range results {
		if keyID == "" {
			keyID, secret = result.Secret.KeyID, result.Secret.Secret
		}
		if result.Secret.KeyID != keyID || result.Secret.Secret != secret {
			t.Fatal("idempotent rotation returned different secret")
		}
		if result.Duplicate {
			duplicateCount++
		} else {
			newCount++
		}
	}
	if newCount != 1 || duplicateCount != concurrency-1 {
		t.Fatalf("new=%d duplicate=%d", newCount, duplicateCount)
	}
	var active, retiring, commands, audits int
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	if err := super.QueryRow(ctx, `SELECT count(*) FILTER(WHERE state='active'),count(*) FILTER(WHERE state='retiring')
		FROM wde.endpoint_secret_versions WHERE workspace_id=$1`, workspaceID).Scan(&active, &retiring); err != nil {
		t.Fatal(err)
	}
	if err := super.QueryRow(ctx, `SELECT (SELECT count(*) FROM wde.secret_rotation_commands WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='endpoint.secret_rotate')`, workspaceID).
		Scan(&commands, &audits); err != nil {
		t.Fatal(err)
	}
	if active != 1 || retiring != 1 || commands != 1 || audits != 1 {
		t.Fatalf("active=%d retiring=%d commands=%d audits=%d", active, retiring, commands, audits)
	}
	published, err := event.NewService(event.NewPostgresStore(api), materials).Publish(ctx, workspaceID,
		"rotation-event", []byte(`{"type":"rotation.test","data":{}}`))
	if err != nil || len(published.DeliveryIDs) != 1 {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	claim := claimExactlyOne(t, ctx, delivery.NewPostgresStore(worker), uuid.New())
	if claim.DeliveryID != published.DeliveryIDs[0] || claim.Retiring == nil || claim.Retiring.KeyID == claim.KeyID {
		t.Fatalf("dual-sign claim=%+v", claim)
	}
	if _, err := super.Exec(ctx, `UPDATE wde.endpoint_secret_versions SET retire_at=valid_from+interval '1 microsecond'
		WHERE workspace_id=$1 AND state='retiring'`, workspaceID); err != nil {
		t.Fatal(err)
	}
	var secretsPurged int
	if err := worker.QueryRow(ctx, `SELECT wde.purge_retired_secrets(100)`).Scan(&secretsPurged); err != nil || secretsPurged != 1 {
		t.Fatalf("secrets purged=%d err=%v", secretsPurged, err)
	}
	var allCleared bool
	if err := super.QueryRow(ctx, `SELECT bool_and(cipher_format_version IS NULL AND secret_ciphertext IS NULL
		AND secret_nonce IS NULL AND kek_version IS NULL AND purged_at IS NOT NULL)
		FROM wde.endpoint_secret_versions WHERE workspace_id=$1 AND state='purged'`, workspaceID).Scan(&allCleared); err != nil || !allCleared {
		t.Fatalf("retired envelope cleared=%v err=%v", allCleared, err)
	}
	var secretAudits, commandsForPurgedSecrets int
	if err := super.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='secret.purge'),
		(SELECT count(*) FROM wde.secret_rotation_commands c JOIN wde.endpoint_secret_versions s
		 ON s.workspace_id=c.workspace_id AND s.id=c.secret_version_id
		 WHERE c.workspace_id=$1 AND s.state='purged')`, workspaceID).
		Scan(&secretAudits, &commandsForPurgedSecrets); err != nil {
		t.Fatal(err)
	}
	if secretAudits != 1 || commandsForPurgedSecrets != 0 {
		t.Fatalf("secret audits=%d commands for purged secrets=%d", secretAudits, commandsForPurgedSecrets)
	}
	var auditText string
	if err := super.QueryRow(ctx, `SELECT COALESCE(string_agg(row_to_json(a)::text,''),'')
		FROM wde.audit_events a WHERE workspace_id=$1`, workspaceID).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditText, secret) || strings.Contains(auditText, "ciphertext") {
		t.Fatal("rotation audit exposed secret material")
	}
}

func TestExpiredRotationIdempotencySurvivesSecretPurge(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	workspaceID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'rotation-expiry-s4')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	service := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials)
	created, err := service.Create(ctx, workspaceID, endpoint.CreateInput{
		URL: "http://127.0.0.1:18081/success", EventTypes: []string{"rotation.expiry"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Rotate(ctx, workspaceID, created.Endpoint.ID, "actor-a", "request-a",
		"rotation-key-a", endpoint.RotationInput{OverlapSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	expireRetiringSecrets := func() {
		if _, expireErr := super.Exec(ctx, `UPDATE wde.endpoint_secret_versions
			SET retire_at=valid_from+interval '1 microsecond'
			WHERE workspace_id=$1 AND state='retiring'`, workspaceID); expireErr != nil {
			t.Fatal(expireErr)
		}
	}
	expireRetiringSecrets()
	if _, err = service.Rotate(ctx, workspaceID, created.Endpoint.ID, "actor-b", "request-b",
		"rotation-key-b", endpoint.RotationInput{OverlapSeconds: 3600}); err != nil {
		t.Fatal(err)
	}
	expireRetiringSecrets()
	var purged int
	if err = worker.QueryRow(ctx, `SELECT wde.purge_retired_secrets(100)`).Scan(&purged); err != nil || purged != 1 {
		t.Fatalf("purged=%d err=%v", purged, err)
	}
	var secretsBefore, commandsBefore, rotationAuditsBefore, purgeAuditsBefore, purgedCommands int
	if err = super.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wde.endpoint_secret_versions WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.secret_rotation_commands WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='endpoint.secret_rotate'),
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='secret.purge'),
		(SELECT count(*) FROM wde.secret_rotation_commands c JOIN wde.endpoint_secret_versions s
		 ON s.workspace_id=c.workspace_id AND s.id=c.secret_version_id
		 WHERE c.workspace_id=$1 AND s.state='purged')`, workspaceID).Scan(
		&secretsBefore, &commandsBefore, &rotationAuditsBefore, &purgeAuditsBefore, &purgedCommands); err != nil {
		t.Fatal(err)
	}
	if commandsBefore != 2 || purgedCommands != 1 {
		t.Fatalf("commands=%d purged_commands=%d", commandsBefore, purgedCommands)
	}
	_, err = service.Rotate(ctx, workspaceID, created.Endpoint.ID, "actor-a", "request-a-retry",
		"rotation-key-a", endpoint.RotationInput{OverlapSeconds: 3600})
	if !errors.Is(err, endpoint.ErrRotationExpired) {
		t.Fatalf("expired duplicate err=%v", err)
	}
	_, err = service.Rotate(ctx, workspaceID, created.Endpoint.ID, "actor-a", "request-a-conflict",
		"rotation-key-a", endpoint.RotationInput{OverlapSeconds: 7200})
	if !errors.Is(err, endpoint.ErrRotationConflict) {
		t.Fatalf("expired conflict err=%v", err)
	}
	var secretsAfter, commandsAfter, rotationAuditsAfter, purgeAuditsAfter int
	if err = super.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wde.endpoint_secret_versions WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.secret_rotation_commands WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='endpoint.secret_rotate'),
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='secret.purge')`, workspaceID).Scan(
		&secretsAfter, &commandsAfter, &rotationAuditsAfter, &purgeAuditsAfter); err != nil {
		t.Fatal(err)
	}
	if secretsAfter != secretsBefore || commandsAfter != commandsBefore ||
		rotationAuditsAfter != rotationAuditsBefore || purgeAuditsAfter != purgeAuditsBefore {
		t.Fatalf("retry mutated state before=(%d,%d,%d,%d) after=(%d,%d,%d,%d)",
			secretsBefore, commandsBefore, rotationAuditsBefore, purgeAuditsBefore,
			secretsAfter, commandsAfter, rotationAuditsAfter, purgeAuditsAfter)
	}
}

func TestSecretStateAndTemporalConstraintsFailClosedForAPIAndOwner(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	workspaceID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'secret-constraints-s4')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	created, err := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials).
		Create(ctx, workspaceID, endpoint.CreateInput{URL: "http://127.0.0.1:18081/success", EventTypes: []string{"secret.constraint"}})
	if err != nil {
		t.Fatal(err)
	}
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	var secretID uuid.UUID
	if err := super.QueryRow(ctx, `SELECT id FROM wde.endpoint_secret_versions
		WHERE workspace_id=$1 AND endpoint_id=$2 AND state='active'`, workspaceID, created.Endpoint.ID).Scan(&secretID); err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		`UPDATE wde.endpoint_secret_versions SET retire_at=transaction_timestamp() WHERE id=$1`,
		`UPDATE wde.endpoint_secret_versions SET state='retired' WHERE id=$1`,
		`UPDATE wde.endpoint_secret_versions SET cipher_format_version=NULL,
			secret_nonce=NULL,kek_version=NULL WHERE id=$1`,
		`UPDATE wde.endpoint_secret_versions SET state='retiring',retire_at=valid_from WHERE id=$1`,
		`UPDATE wde.endpoint_secret_versions SET state='purged',cipher_format_version=NULL,
			secret_ciphertext=NULL,secret_nonce=NULL,kek_version=NULL,purged_at=valid_from-interval '1 second' WHERE id=$1`,
	}
	for _, statement := range invalid {
		if _, err := super.Exec(ctx, statement, secretID); err == nil {
			t.Fatalf("invalid transition accepted: %s", statement)
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
	if _, err = tx.Exec(ctx, `SAVEPOINT reject_null_rotation`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `SELECT * FROM wde.rotate_endpoint_secret(
		$1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'nullformat'::text,NULL::smallint,
		$6::bytea,$7::bytea,1::smallint,'api-test'::text,'req-null'::text,$8::bytea,$9::bytea,3600::integer)`,
		uuid.New(), uuid.New(), workspaceID, created.Endpoint.ID, uuid.New(), make([]byte, 16),
		make([]byte, 12), make([]byte, 32), make([]byte, 32))
	if err == nil || !strings.Contains(err.Error(), "rotation_invalid") {
		t.Fatalf("NULL rotation fields did not fail validation: %v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT reject_null_rotation`); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO wde.endpoint_secret_versions(id,workspace_id,endpoint_id,key_id,state,
		cipher_format_version,secret_ciphertext,secret_nonce,kek_version)
		VALUES($1,$2,$3,'invalid-api-secret','retiring',2,$4,$5,1)`, uuid.New(), workspaceID,
		created.Endpoint.ID, make([]byte, 16), make([]byte, 12))
	if err == nil {
		t.Fatal("API INSERT bypassed secret state invariant")
	}
}

func TestPayloadPurgeFencesWorkerAndBlocksReplay(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "purge-s4", 1)
	store := delivery.NewPostgresStore(worker)
	workerID := uuid.New()
	claim := claimExactlyOne(t, ctx, store, workerID)
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	if _, err := super.Exec(ctx, `UPDATE wde.events SET payload_expires_at=transaction_timestamp()-interval '1 second'
		WHERE workspace_id=$1`, seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	var purged int
	if err := worker.QueryRow(ctx, `SELECT wde.purge_expired_payloads(100)`).Scan(&purged); err != nil || purged != 1 {
		t.Fatalf("purged=%d err=%v", purged, err)
	}
	var status, category, attemptState string
	var fencing int64
	var envelopeCleared bool
	if err := super.QueryRow(ctx, `SELECT d.status,d.last_error_category,d.fencing_token,a.state,
		(e.payload_ciphertext IS NULL AND e.payload_nonce IS NULL AND e.payload_kek_version IS NULL
		 AND e.payload_cipher_format_version IS NULL AND e.payload_purged_at IS NOT NULL)
		FROM wde.deliveries d JOIN wde.events e ON e.id=d.event_id
		JOIN wde.delivery_attempts a ON a.delivery_id=d.id WHERE d.id=$1`, claim.DeliveryID).
		Scan(&status, &category, &fencing, &attemptState, &envelopeCleared); err != nil {
		t.Fatal(err)
	}
	if status != "failed_permanent" || category != "payload_expired" || fencing <= claim.FencingToken ||
		attemptState != "abandoned" || !envelopeCleared {
		t.Fatalf("status=%s category=%s fencing=%d attempt=%s cleared=%v", status, category, fencing, attemptState, envelopeCleared)
	}
	_, err := delivery.NewReplayService(delivery.NewPostgresStore(api), materials).Request(ctx,
		seeded.workspaceID, claim.DeliveryID, uuid.New(), "purged-replay", "operator retry", "req-purge")
	if !errors.Is(err, delivery.ErrPayloadPurged) {
		t.Fatalf("replay err=%v", err)
	}
	changed, finalizeErr := store.Finalize(ctx, claim, workerID, delivery.Result{Disposition: delivery.DispositionSuccess})
	if finalizeErr != nil || changed {
		t.Fatalf("stale worker changed=%v err=%v", changed, finalizeErr)
	}
	var auditText string
	if err := super.QueryRow(ctx, `SELECT COALESCE(string_agg(row_to_json(a)::text,''),'')
		FROM wde.audit_events a WHERE workspace_id=$1`, seeded.workspaceID).Scan(&auditText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(auditText, `"index"`) || strings.Contains(auditText, "ciphertext") {
		t.Fatal("payload purge audit exposed payload material")
	}
}

func TestMaintenanceBatchesRejectNullAndEnforcePhysicalCap(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "batch-guard-s4", 1)
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	if _, err := super.Exec(ctx, `UPDATE wde.events SET payload_expires_at=transaction_timestamp()-interval '1 second'
		WHERE workspace_id=$1`, seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `UPDATE wde.endpoint_secret_versions SET state='retiring',
		retire_at=valid_from+interval '1 microsecond' WHERE workspace_id=$1 AND state='active'`,
		seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	if changed, err := retention.NewAdminStore(admin).RequestWorkspacePurge(
		ctx, seeded.workspaceID, "req-batch-guard"); err != nil || !changed {
		t.Fatalf("request changed=%v err=%v", changed, err)
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.rate_limit_buckets(
		dimension_hash,dimension_type,operation,window_started_at,workspace_id,count,expires_at)
		SELECT decode(md5($1::uuid::text||value::text)||md5(value::text||$1::uuid::text),'hex'),
		'workspace','query',transaction_timestamp()-interval '4 hours',$1::uuid,1,
		transaction_timestamp()-interval '3 hours' FROM generate_series(1,1101) value`, seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	readState := func() [7]int {
		var state [7]int
		if err := super.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM wde.events WHERE workspace_id=$1 AND payload_ciphertext IS NOT NULL),
			(SELECT count(*) FROM wde.endpoint_secret_versions WHERE workspace_id=$1 AND state='retiring'),
			(SELECT count(*) FROM wde.rate_limit_buckets WHERE workspace_id=$1),
			(SELECT attempts FROM wde.maintenance_jobs WHERE workspace_id=$1),
			(SELECT generation FROM wde.restore_control WHERE singleton),
			(SELECT count(*) FROM wde.replay_commands WHERE workspace_id=$1),
			(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1)`, seeded.workspaceID).Scan(
			&state[0], &state[1], &state[2], &state[3], &state[4], &state[5], &state[6]); err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := readState()
	queries := []struct{ query, want string }{
		{`SELECT wde.purge_expired_payloads(NULL::integer)`, "invalid purge batch"},
		{`SELECT wde.purge_retired_secrets(NULL::integer)`, "invalid purge batch"},
		{`SELECT * FROM wde.purge_expired_metadata(NULL::integer)`, "invalid metadata purge batch"},
		{`SELECT * FROM wde.purge_workspace(NULL::integer)`, "invalid workspace purge batch"},
		{`SELECT wde.purge_expired_payloads(1001)`, "invalid purge batch"},
		{`SELECT wde.purge_retired_secrets(1001)`, "invalid purge batch"},
		{`SELECT * FROM wde.purge_expired_metadata(1001)`, "invalid metadata purge batch"},
		{`SELECT * FROM wde.purge_workspace(1001)`, "invalid workspace purge batch"},
	}
	for _, probe := range queries {
		if _, err := worker.Exec(ctx, probe.query); err == nil || !strings.Contains(err.Error(), probe.want) {
			t.Fatalf("unsafe batch result query=%s err=%v", probe.query, err)
		}
	}
	adminQueries := []struct{ query, want string }{
		{`SELECT wde.enter_restore_quarantine(NULL::bigint)`, "invalid restore generation"},
		{`SELECT wde.complete_restore_reconciliation(NULL::bigint)`, "invalid restore generation"},
	}
	for _, probe := range adminQueries {
		if _, err := admin.Exec(ctx, probe.query); err == nil || !strings.Contains(err.Error(), probe.want) {
			t.Fatalf("unsafe nullable result query=%s err=%v", probe.query, err)
		}
	}
	if _, err := admin.Exec(ctx, `SELECT wde.request_workspace_purge($1,NULL::text)`, seeded.workspaceID); err == nil ||
		!strings.Contains(err.Error(), "invalid workspace purge request") {
		t.Fatalf("NULL workspace purge request id err=%v", err)
	}
	if _, err := admin.Exec(ctx, `SELECT wde.append_audit_event($2::uuid,$1::uuid,NULL::text,'actor',
		'workspace.purge','workspace',$1::uuid::text,'request','accepted',NULL)`, seeded.workspaceID, uuid.New()); err == nil ||
		!strings.Contains(err.Error(), "invalid audit event") {
		t.Fatalf("NULL audit actor type err=%v", err)
	}
	if _, err := api.Exec(ctx, `SELECT * FROM wde.consume_quota($1::bytea,'global'::text,'query'::text,
		NULL::uuid,NULL::uuid,NULL::uuid,NULL::integer,60::integer)`, make([]byte, 32)); err == nil ||
		!strings.Contains(err.Error(), "invalid quota request") {
		t.Fatalf("NULL quota limit err=%v", err)
	}
	if _, err := api.Exec(ctx, `SELECT * FROM wde.request_replay($1::uuid,$2::uuid,$3::uuid,$4::uuid,
		NULL::text,$5::bytea,$6::bytea,1::smallint,'reason'::text,'request'::text)`,
		uuid.New(), uuid.New(), seeded.workspaceID, uuid.New(), make([]byte, 32), make([]byte, 32)); err == nil ||
		!strings.Contains(err.Error(), "replay_invalid") {
		t.Fatalf("NULL replay actor err=%v", err)
	}
	if after := readState(); after != before {
		t.Fatalf("rejected calls mutated state before=%v after=%v", before, after)
	}
	var attempts, replays, rotations, deliveries, events, audits, buckets int
	if err := worker.QueryRow(ctx, `SELECT * FROM wde.purge_expired_metadata(1000)`).Scan(
		&attempts, &replays, &rotations, &deliveries, &events, &audits, &buckets); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := super.QueryRow(ctx, `SELECT count(*) FROM wde.rate_limit_buckets WHERE workspace_id=$1`,
		seeded.workspaceID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if buckets != 1000 || remaining != 101 {
		t.Fatalf("physical cap buckets=%d remaining=%d", buckets, remaining)
	}
	var completed bool
	for tries := 0; !completed && tries < 25; tries++ {
		var id uuid.UUID
		var deleted int
		var checkpoint string
		if err := worker.QueryRow(ctx, `SELECT * FROM wde.purge_workspace(1000)`).Scan(
			&id, &deleted, &completed, &checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if !completed {
		t.Fatal("batch guard workspace cleanup did not complete")
	}
}

func TestRetentionRunnerDrainsLargeExpiredBucketBacklog(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	if _, err := super.Exec(ctx, `UPDATE wde.restore_control SET audit_retention_days=731 WHERE singleton`); err == nil {
		t.Fatal("audit retention above 730 days accepted")
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.rate_limit_buckets(
		dimension_hash,dimension_type,operation,window_started_at,count,expires_at)
		VALUES($1,'global','query',transaction_timestamp(),1,transaction_timestamp()+interval '8 days')`,
		make([]byte, 32)); err == nil {
		t.Fatal("rate-limit retention above seven days accepted")
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.rate_limit_buckets(
		dimension_hash,dimension_type,operation,window_started_at,count,expires_at)
		SELECT decode(lpad(to_hex(value),64,'0'),'hex'),'global','query',
			transaction_timestamp()-interval '2 hours',1,transaction_timestamp()-interval '1 hour'
		FROM generate_series(1,2501) value ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	runner := retention.NewRunner(retention.NewPostgresStore(worker), time.Hour)
	report := runner.RunOnce(ctx)
	var remaining int
	if err := super.QueryRow(ctx, `SELECT count(*) FROM wde.rate_limit_buckets
		WHERE expires_at<=transaction_timestamp()`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if report.Counts.Buckets < 2501 || report.Batches < 27 || report.Backlog != 0 ||
		report.OldestAge != 0 || remaining != 0 || runner.Ready() != nil {
		t.Fatalf("report=%+v remaining=%d ready=%v", report, remaining, runner.Ready())
	}
}

func TestRetentionBacklogExcludesParentsBlockedByLiveChildren(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "retention-parent-s4", 1)
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	if _, err := super.Exec(ctx, `UPDATE wde.workspaces SET metadata_retention_days=30 WHERE id=$1`,
		seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `UPDATE wde.events SET created_at=transaction_timestamp()-interval '31 days',
		payload_cipher_format_version=NULL,payload_ciphertext=NULL,payload_nonce=NULL,payload_kek_version=NULL,
		payload_purged_at=transaction_timestamp()-interval '1 day' WHERE workspace_id=$1`, seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	runner := retention.NewRunner(retention.NewPostgresStore(worker), time.Hour)
	report := runner.RunOnce(ctx)
	var graphRows int
	if err := super.QueryRow(ctx, `SELECT (SELECT count(*) FROM wde.events WHERE workspace_id=$1)+
		(SELECT count(*) FROM wde.deliveries WHERE workspace_id=$1)`, seeded.workspaceID).Scan(&graphRows); err != nil {
		t.Fatal(err)
	}
	if report.Backlog != 0 || report.Degraded || runner.Ready() != nil || graphRows != 2 {
		t.Fatalf("blocked parent was treated as actionable: report=%+v graph=%d ready=%v",
			report, graphRows, runner.Ready())
	}
}

func TestMetadataRetentionPurgesGraphCommandsAndExpiredAudit(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "metadata-retention-s4", 1)
	store := delivery.NewPostgresStore(worker)
	workerID := uuid.New()
	claim := claimExactlyOne(t, ctx, store, workerID)
	if changed, err := store.Finalize(ctx, claim, workerID, delivery.Result{Disposition: delivery.DispositionSuccess}); err != nil || !changed {
		t.Fatalf("finalize changed=%v err=%v", changed, err)
	}
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	var endpointID, secretID, auditID uuid.UUID
	var keyID string
	if err := super.QueryRow(ctx, `SELECT d.endpoint_id,s.id,s.key_id,
		(SELECT id FROM wde.audit_events WHERE workspace_id=d.workspace_id ORDER BY created_at LIMIT 1)
		FROM wde.deliveries d JOIN wde.endpoint_secret_versions s
		 ON s.workspace_id=d.workspace_id AND s.endpoint_id=d.endpoint_id AND s.state='active'
		WHERE d.id=$1`, claim.DeliveryID).Scan(&endpointID, &secretID, &keyID, &auditID); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`UPDATE wde.workspaces SET metadata_retention_days=30 WHERE id=$1`,
		`UPDATE wde.delivery_attempts SET started_at=transaction_timestamp()-interval '31 days',
			finished_at=transaction_timestamp()-interval '31 days' WHERE workspace_id=$1`,
		`UPDATE wde.deliveries SET created_at=transaction_timestamp()-interval '31 days',
			updated_at=transaction_timestamp()-interval '31 days',terminal_at=transaction_timestamp()-interval '31 days'
			WHERE workspace_id=$1`,
		`UPDATE wde.events SET created_at=transaction_timestamp()-interval '31 days',
			payload_cipher_format_version=NULL,payload_ciphertext=NULL,payload_nonce=NULL,payload_kek_version=NULL,
			payload_purged_at=transaction_timestamp()-interval '31 days' WHERE workspace_id=$1`,
	}
	for _, statement := range statements {
		if _, err := super.Exec(ctx, statement, seeded.workspaceID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := super.Exec(ctx, `UPDATE wde.audit_events SET created_at=transaction_timestamp()-interval '366 days'
		WHERE id=$1`, auditID); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.replay_commands(id,workspace_id,delivery_id,actor_type,
		actor_id,idempotency_key_hash,fingerprint,fingerprint_version,new_run_number,reason,request_id,result,created_at)
		VALUES($1,$2,$3,'api_key','retention-test',$4,$5,1,2,'expired command','retention-test','accepted',
		transaction_timestamp()-interval '31 days')`, uuid.New(), seeded.workspaceID, claim.DeliveryID,
		make([]byte, 32), make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.secret_rotation_commands(id,workspace_id,endpoint_id,
		secret_version_id,actor_id,idempotency_key_hash,fingerprint,key_id,overlap_seconds,created_at)
		VALUES($1,$2,$3,$4,'retention-test',$5,$6,$7,3600,transaction_timestamp()-interval '31 days')`,
		uuid.New(), seeded.workspaceID, endpointID, secretID, make([]byte, 32), make([]byte, 32), keyID); err != nil {
		t.Fatal(err)
	}
	report := retention.NewRunner(retention.NewPostgresStore(worker), time.Hour).RunOnce(ctx)
	var graphRows, commandRows, auditRows int
	if err := super.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM wde.events WHERE workspace_id=$1)+
		(SELECT count(*) FROM wde.deliveries WHERE workspace_id=$1)+
		(SELECT count(*) FROM wde.delivery_attempts WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.replay_commands WHERE workspace_id=$1)+
		(SELECT count(*) FROM wde.secret_rotation_commands WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.audit_events WHERE id=$2)`, seeded.workspaceID, auditID).
		Scan(&graphRows, &commandRows, &auditRows); err != nil {
		t.Fatal(err)
	}
	if graphRows != 0 || commandRows != 0 || auditRows != 0 || report.Backlog != 0 ||
		report.Counts.Attempts == 0 || report.Counts.Deliveries == 0 || report.Counts.Events == 0 {
		t.Fatalf("graph=%d commands=%d audit=%d report=%+v", graphRows, commandRows, auditRows, report)
	}
}

func TestRestoreQuarantineRevokesSnapshotBeforeReadiness(t *testing.T) {
	superURL, apiURL, adminURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"),
		os.Getenv("WDE_TEST_API_DATABASE_URL"), os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	if superURL == "" || apiURL == "" || adminURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	name := "wde_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := super.Exec(ctx, "CREATE DATABASE "+identifier+" OWNER wde_owner"); err != nil {
		t.Fatal(err)
	}
	defer dropBoundaryDatabase(t, ctx, super, name, identifier)
	if _, err := super.Exec(ctx, "REVOKE ALL ON DATABASE "+identifier+
		" FROM PUBLIC; GRANT CONNECT ON DATABASE "+identifier+" TO wde_migrator,wde_api,wde_worker,wde_admin"); err != nil {
		t.Fatal(err)
	}
	root, _ := filepath.Abs("../..")
	boundarySuperURL := databaseURL(t, superURL, name)
	runGooseBoundary(t, ctx, root, boundarySuperURL, "up-to", 19)
	boundaryAdmin := mustPool(t, ctx, databaseURL(t, adminURL, name))
	defer boundaryAdmin.Close()
	boundaryAPI := mustPool(t, ctx, databaseURL(t, apiURL, name))
	defer boundaryAPI.Close()
	boundarySuper := mustPool(t, ctx, boundarySuperURL)
	defer boundarySuper.Close()
	workspaceID, keyID, endpointID, secretID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := boundaryAdmin.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'pre-revocation-backup')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := boundaryAdmin.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier)
		VALUES($1,$2,'restore012345678',$3)`, keyID, workspaceID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := boundarySuper.Exec(ctx, `INSERT INTO wde.endpoints(id,workspace_id,scheme,host_ascii,port,
		target_cipher_format_version,target_ciphertext,target_nonce,target_kek_version)
		VALUES($1,$2,'https','restore.example',443,2,$3,$4,1)`, endpointID, workspaceID,
		make([]byte, 16), make([]byte, 12)); err != nil {
		t.Fatal(err)
	}
	if _, err := boundarySuper.Exec(ctx, `INSERT INTO wde.endpoint_secret_versions(id,workspace_id,endpoint_id,
		key_id,state,cipher_format_version,secret_ciphertext,secret_nonce,kek_version,valid_from,retire_at)
		VALUES($1,$2,$3,'restore-key','retiring',2,$4,$5,1,transaction_timestamp(),
		transaction_timestamp()+interval '1 hour')`, secretID, workspaceID, endpointID,
		make([]byte, 16), make([]byte, 12)); err != nil {
		t.Fatal(err)
	}
	changed, err := retention.NewAdminStore(boundaryAdmin).QuarantineRestore(ctx, 1)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	changed, err = retention.NewAdminStore(boundaryAdmin).QuarantineRestore(ctx, 1)
	if err != nil || changed {
		t.Fatalf("idempotent changed=%v err=%v", changed, err)
	}
	var keyStatus, workspaceStatus, restoreState, endpointStatus, secretState string
	var secretCleared bool
	var quarantineAudits int
	if err := boundarySuper.QueryRow(ctx, `SELECT k.status,w.status,r.state,e.status,s.state,
		s.retire_at IS NULL AND s.cipher_format_version IS NULL AND s.secret_ciphertext IS NULL
			AND s.secret_nonce IS NULL AND s.kek_version IS NULL AND s.purged_at IS NOT NULL,
		(SELECT count(*) FROM wde.audit_events a WHERE a.action='restore.quarantine'
			AND a.resource_id='1')
		FROM wde.api_keys k JOIN wde.workspaces w ON w.id=k.workspace_id
		JOIN wde.endpoints e ON e.workspace_id=w.id JOIN wde.endpoint_secret_versions s ON s.endpoint_id=e.id
		CROSS JOIN wde.restore_control r WHERE k.id=$1 AND e.id=$2 AND s.id=$3`, keyID, endpointID, secretID).
		Scan(&keyStatus, &workspaceStatus, &restoreState, &endpointStatus, &secretState,
			&secretCleared, &quarantineAudits); err != nil {
		t.Fatal(err)
	}
	if keyStatus != "revoked" || workspaceStatus != "suspended" || restoreState != "quarantined" ||
		endpointStatus != "disabled" || secretState != "purged" || !secretCleared || quarantineAudits != 1 {
		t.Fatalf("key=%s workspace=%s restore=%s endpoint=%s secret=%s cleared=%v audits=%d",
			keyStatus, workspaceStatus, restoreState, endpointStatus, secretState, secretCleared, quarantineAudits)
	}
	if err := database.Check(ctx, boundaryAPI, database.RoleAPI); !errors.Is(err, database.ErrUnavailable) {
		t.Fatalf("quarantined readiness err=%v", err)
	}
	record, found, err := auth.NewPostgresLookup(boundaryAPI).LookupKey(ctx, "restore012345678")
	if err != nil || !found || record.Status != "revoked" {
		t.Fatalf("found=%v status=%s err=%v", found, record.Status, err)
	}
	if changed, err = retention.NewAdminStore(boundaryAdmin).CompleteRestore(ctx, 1); err != nil || !changed {
		t.Fatalf("reconcile changed=%v err=%v", changed, err)
	}
	if err := database.Check(ctx, boundaryAPI, database.RoleAPI); err != nil {
		t.Fatalf("reconciled readiness err=%v", err)
	}
}

func TestWorkspacePurgePreservesTombstoneAndAudit(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "workspace-purge-s4", 205)
	keyID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier)
		VALUES($1,$2,'purge01234567890',$3)`, keyID, seeded.workspaceID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
	changed, err := retention.NewAdminStore(admin).RequestWorkspacePurge(ctx, seeded.workspaceID, "req-workspace-purge")
	if err != nil || !changed {
		t.Fatalf("request changed=%v err=%v", changed, err)
	}
	changed, err = retention.NewAdminStore(admin).RequestWorkspacePurge(ctx, seeded.workspaceID, "req-workspace-purge")
	if err != nil || changed {
		t.Fatalf("duplicate request changed=%v err=%v", changed, err)
	}
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	var generation, acceptedAudits int
	if err := super.QueryRow(ctx, `SELECT
		(SELECT generation FROM wde.workspace_tombstones WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='workspace.purge' AND outcome='accepted')`,
		seeded.workspaceID).Scan(&generation, &acceptedAudits); err != nil {
		t.Fatal(err)
	}
	if generation != 1 || acceptedAudits != 1 {
		t.Fatalf("generation=%d accepted audits=%d", generation, acceptedAudits)
	}

	var purgedID uuid.UUID
	var deleted int64
	var completed bool
	var checkpoint string
	for range 4 {
		if err := worker.QueryRow(ctx, `SELECT * FROM wde.purge_workspace(25)`).Scan(
			&purgedID, &deleted, &completed, &checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if purgedID != seeded.workspaceID || completed {
		t.Fatalf("early purge id=%s completed=%v checkpoint=%s", purgedID, completed, checkpoint)
	}
	var beforeRows int64
	if err := super.QueryRow(ctx, `SELECT processed_rows FROM wde.maintenance_jobs
		WHERE workspace_id=$1 AND job_type='purge_workspace'`, seeded.workspaceID).Scan(&beforeRows); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := worker.QueryRow(cancelled, `SELECT * FROM wde.purge_workspace(25)`).Scan(
		&purgedID, &deleted, &completed, &checkpoint); err == nil {
		t.Fatal("cancelled purge unexpectedly advanced")
	}
	var afterRows int64
	if err := super.QueryRow(ctx, `SELECT processed_rows FROM wde.maintenance_jobs
		WHERE workspace_id=$1 AND job_type='purge_workspace'`, seeded.workspaceID).Scan(&afterRows); err != nil || afterRows != beforeRows {
		t.Fatalf("cancelled progress before=%d after=%d err=%v", beforeRows, afterRows, err)
	}
	if _, err := super.Exec(ctx, `UPDATE wde.maintenance_jobs SET status='processing',lease_owner=$2,
		lease_expires_at=transaction_timestamp()-interval '1 second' WHERE workspace_id=$1`,
		seeded.workspaceID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := worker.QueryRow(ctx, `SELECT * FROM wde.purge_workspace(25)`).Scan(
		&purgedID, &deleted, &completed, &checkpoint); err != nil {
		t.Fatalf("expired lease was not reclaimed: %v", err)
	}
	for attempts := 0; !completed && attempts < 40; attempts++ {
		if err := worker.QueryRow(ctx, `SELECT * FROM wde.purge_workspace(25)`).Scan(
			&purgedID, &deleted, &completed, &checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if !completed {
		t.Fatalf("purge did not complete checkpoint=%s", checkpoint)
	}
	var workspaceRows, auditRows int
	var tombstoneComplete bool
	if err := super.QueryRow(ctx, `SELECT (SELECT count(*) FROM wde.workspaces WHERE id=$1),
		(SELECT purge_completed_at IS NOT NULL FROM wde.workspace_tombstones WHERE workspace_id=$1),
		(SELECT count(*) FROM wde.audit_events WHERE workspace_id=$1 AND action='workspace.purge')`, seeded.workspaceID).
		Scan(&workspaceRows, &tombstoneComplete, &auditRows); err != nil {
		t.Fatal(err)
	}
	if workspaceRows != 0 || !tombstoneComplete || auditRows != 2 {
		t.Fatalf("workspaces=%d tombstone=%v audits=%d", workspaceRows, tombstoneComplete, auditRows)
	}
}

func TestWorkspacePurgeSerializesRotationAndRewindsLateChildren(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	suspendActiveWorkspaces(t, ctx, admin)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	seeded := seedDeliveries(t, ctx, api, admin, materials, "workspace-rotation-s4", 1)
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	var endpointID, secretID uuid.UUID
	var keyID string
	if err := super.QueryRow(ctx, `SELECT d.endpoint_id,s.id,s.key_id FROM wde.deliveries d
		JOIN wde.endpoint_secret_versions s ON s.workspace_id=d.workspace_id
		 AND s.endpoint_id=d.endpoint_id AND s.state='active' WHERE d.workspace_id=$1 LIMIT 1`,
		seeded.workspaceID).Scan(&endpointID, &secretID, &keyID); err != nil {
		t.Fatal(err)
	}
	tx, err := api.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('wde.workspace_id',$1,true)`, seeded.workspaceID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(
		hashtextextended($1::text||':'||$2::text,1))`, seeded.workspaceID, endpointID); err != nil {
		t.Fatal(err)
	}
	type purgeResult struct {
		changed bool
		err     error
	}
	result := make(chan purgeResult, 1)
	go func() {
		changed, purgeErr := retention.NewAdminStore(admin).RequestWorkspacePurge(
			ctx, seeded.workspaceID, "req-workspace-rotation")
		result <- purgeResult{changed: changed, err: purgeErr}
	}()
	select {
	case early := <-result:
		t.Fatalf("purge bypassed active rotation lock: %+v", early)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case completed := <-result:
		if completed.err != nil || !completed.changed {
			t.Fatalf("purge request changed=%v err=%v", completed.changed, completed.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("purge request remained blocked after rotation released")
	}
	service := endpoint.NewService(endpoint.NewPostgresStore(api), config.ProfileTest, true, materials)
	if _, err := service.Rotate(ctx, seeded.workspaceID, endpointID, "rotation-race", "req-race",
		"rotation-after-delete", endpoint.RotationInput{OverlapSeconds: 3600}); !errors.Is(err, endpoint.ErrNotFound) {
		t.Fatalf("rotation after purge request err=%v", err)
	}
	if _, err := super.Exec(ctx, `INSERT INTO wde.secret_rotation_commands(id,workspace_id,endpoint_id,
		secret_version_id,actor_id,idempotency_key_hash,fingerprint,key_id,overlap_seconds)
		VALUES($1,$2,$3,$4,'late-rotation',$5,$6,$7,3600)`,
		uuid.New(), seeded.workspaceID, endpointID, secretID, make([]byte, 32), make([]byte, 32), keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(ctx, `UPDATE wde.maintenance_jobs SET checkpoint='secrets' WHERE workspace_id=$1`,
		seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	var purgedID uuid.UUID
	var deleted int64
	var completed bool
	var checkpoint string
	if err := worker.QueryRow(ctx, `SELECT * FROM wde.purge_workspace(100)`).Scan(
		&purgedID, &deleted, &completed, &checkpoint); err != nil || deleted != 1 {
		t.Fatalf("late child was not rewound id=%s deleted=%d checkpoint=%s err=%v",
			purgedID, deleted, checkpoint, err)
	}
	for attempts := 0; !completed && attempts < 20; attempts++ {
		if err := worker.QueryRow(ctx, `SELECT * FROM wde.purge_workspace(100)`).Scan(
			&purgedID, &deleted, &completed, &checkpoint); err != nil {
			t.Fatal(err)
		}
	}
	if !completed {
		t.Fatalf("purge remained stuck after rewind checkpoint=%s", checkpoint)
	}
}

func TestDataSecurityFunctionsAndRolesAreLeastPrivilege(t *testing.T) {
	superURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL")
	if superURL == "" {
		t.Skip("integration database URL is not configured")
	}
	ctx := context.Background()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	functions := []struct {
		signature, owner, executor string
		timeout                    bool
	}{
		{"wde.rotate_endpoint_secret(uuid,uuid,uuid,uuid,uuid,text,smallint,bytea,bytea,smallint,text,text,bytea,bytea,integer)", "wde_rotation_executor", "wde_api", false},
		{"wde.purge_expired_payloads(integer)", "wde_maintenance_executor", "wde_worker", true},
		{"wde.purge_retired_secrets(integer)", "wde_maintenance_executor", "wde_worker", true},
		{"wde.purge_expired_metadata(integer)", "wde_maintenance_executor", "wde_worker", true},
		{"wde.retention_backlog()", "wde_maintenance_executor", "wde_worker", true},
		{"wde.purge_workspace(integer)", "wde_maintenance_executor", "wde_worker", true},
		{"wde.request_workspace_purge(uuid,text)", "wde_maintenance_executor", "wde_admin", false},
		{"wde.enter_restore_quarantine(bigint)", "wde_maintenance_executor", "wde_admin", false},
		{"wde.complete_restore_reconciliation(bigint)", "wde_maintenance_executor", "wde_admin", false},
	}
	for _, function := range functions {
		var owner string
		var securityDefiner, publicExecute, expectedExecute bool
		var settings []string
		if err := super.QueryRow(ctx, `SELECT pg_get_userbyid(proowner),prosecdef,COALESCE(proconfig,ARRAY[]::text[]),
			has_function_privilege('public',oid,'EXECUTE'),has_function_privilege($2,oid,'EXECUTE')
			FROM pg_proc WHERE oid=to_regprocedure($1)`, function.signature, function.executor).
			Scan(&owner, &securityDefiner, &settings, &publicExecute, &expectedExecute); err != nil {
			t.Fatal(err)
		}
		hasSearchPath, hasTimeout := false, false
		for _, setting := range settings {
			hasSearchPath = hasSearchPath || setting == "search_path=pg_catalog"
			hasTimeout = hasTimeout || setting == "statement_timeout=5s"
		}
		if owner != function.owner || !securityDefiner || publicExecute || !expectedExecute ||
			!hasSearchPath || (function.timeout && !hasTimeout) {
			t.Fatalf("function=%s owner=%s definer=%v settings=%v public=%v expected=%v",
				function.signature, owner, securityDefiner, settings, publicExecute, expectedExecute)
		}
	}
	var hardened, apiRotationDML, workerJobsDML, adminJobsDML bool
	if err := super.QueryRow(ctx, `SELECT
		(SELECT count(*)=2 FROM pg_roles WHERE rolname IN('wde_rotation_executor','wde_maintenance_executor')
		 AND NOT rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolbypassrls),
		has_table_privilege('wde_api','wde.secret_rotation_commands','INSERT'),
		has_table_privilege('wde_worker','wde.maintenance_jobs','UPDATE'),
		has_table_privilege('wde_admin','wde.maintenance_jobs','UPDATE')`).Scan(
		&hardened, &apiRotationDML, &workerJobsDML, &adminJobsDML); err != nil {
		t.Fatal(err)
	}
	if !hardened || apiRotationDML || workerJobsDML || adminJobsDML {
		t.Fatalf("hardened=%v direct DML api=%v worker=%v admin=%v", hardened, apiRotationDML, workerJobsDML, adminJobsDML)
	}
}
