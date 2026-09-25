package integration

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/consolesession"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/insights"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConsoleSessionACLRevocationAndTenantMetrics(t *testing.T) {
	consoleURL, superURL := os.Getenv("WDE_TEST_CONSOLE_DATABASE_URL"), os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL")
	if consoleURL == "" || superURL == "" {
		t.Skip("console integration database URLs are not configured")
	}
	ctx := context.Background()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	consoleDB := mustPool(t, ctx, consoleURL)
	defer consoleDB.Close()
	materials, err := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, verifier, err := auth.Generate(true, materials.AuthPepper)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, keyID := uuid.New(), uuid.New()
	if _, err = super.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'console-integration')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = super.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier) VALUES($1,$2,$3,$4)`,
		keyID, workspaceID, prefix, verifier[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = super.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope)
		SELECT $1,$2,unnest(ARRAY['deliveries:read','deliveries:retry','endpoints:write','events:write'])`,
		workspaceID, keyID); err != nil {
		t.Fatal(err)
	}
	authenticator := auth.NewAuthenticator(auth.NewPostgresLookup(consoleDB), materials.AuthPepper)
	sessions := consolesession.New(consolesession.NewPostgresStore(consoleDB), authenticator,
		materials.SessionPepper, materials.CSRFPepper)
	session, err := sessions.Login(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := sessions.Authenticate(ctx, session.Token)
	if err != nil || resumed.Principal.WorkspaceID != workspaceID || len(resumed.Principal.Scopes) != 4 {
		t.Fatalf("resumed=%#v err=%v", resumed, err)
	}
	if err = ratelimit.New(consoleDB, materials.RateLimitPepper).Consume(ctx, resumed.Principal, uuid.Nil,
		ratelimit.Policy{Operation: "query", Global: 100, Workspace: 100, APIKey: 100, Window: time.Minute}); err != nil {
		t.Fatalf("console quota failed: %v", err)
	}
	if snapshot, snapshotErr := insights.NewPostgresStore(consoleDB).Snapshot(ctx, workspaceID); snapshotErr != nil ||
		snapshot.Window != "24h" || len(snapshot.Series) != 24 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, snapshotErr)
	}
	var count int
	err = consoleDB.QueryRow(ctx, `SELECT count(*) FROM wde.console_sessions`).Scan(&count)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
		t.Fatalf("direct session read error=%v, want insufficient_privilege", err)
	}
	if _, err = super.Exec(ctx, `UPDATE wde.api_keys SET status='revoked',revoked_at=clock_timestamp() WHERE id=$1`, keyID); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.Authenticate(ctx, session.Token); !errors.Is(err, consolesession.ErrUnauthorized) {
		t.Fatalf("revoked API key resumed session: %v", err)
	}
}

func TestConsoleSessionConcurrencyExpirationAndSlidingWindow(t *testing.T) {
	consoleURL, superURL := os.Getenv("WDE_TEST_CONSOLE_DATABASE_URL"), os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL")
	if consoleURL == "" || superURL == "" {
		t.Skip("console integration database URLs are not configured")
	}
	ctx := context.Background()
	super := mustPool(t, ctx, superURL)
	defer super.Close()
	consoleDB := mustPool(t, ctx, consoleURL)
	defer consoleDB.Close()
	materials, err := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, verifier, err := auth.Generate(true, materials.AuthPepper)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, keyID := uuid.New(), uuid.New()
	if _, err = super.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'console-session-lifecycle')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = super.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier)
		VALUES($1,$2,$3,$4)`, keyID, workspaceID, prefix, verifier[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = super.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope)
		VALUES($1,$2,'deliveries:read')`, workspaceID, keyID); err != nil {
		t.Fatal(err)
	}
	sessions := consolesession.New(consolesession.NewPostgresStore(consoleDB),
		auth.NewAuthenticator(auth.NewPostgresLookup(consoleDB), materials.AuthPepper),
		materials.SessionPepper, materials.CSRFPepper)

	const attempts = 20
	results := make(chan consolesession.Session, attempts)
	errorsFound := make(chan error, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			session, loginErr := sessions.Login(ctx, token)
			if loginErr != nil {
				errorsFound <- loginErr
				return
			}
			results <- session
		}()
	}
	group.Wait()
	close(results)
	close(errorsFound)
	created := make([]consolesession.Session, 0, 5)
	for session := range results {
		created = append(created, session)
	}
	limits := 0
	for loginErr := range errorsFound {
		if !errors.Is(loginErr, consolesession.ErrLimit) {
			t.Fatalf("unexpected concurrent login error: %v", loginErr)
		}
		limits++
	}
	if len(created) != 5 || limits != attempts-5 {
		t.Fatalf("sessions=%d limits=%d", len(created), limits)
	}
	seenTokens := make(map[string]struct{}, len(created))
	for _, session := range created {
		if _, duplicate := seenTokens[session.Token]; duplicate {
			t.Fatal("session fixation: duplicate browser token")
		}
		seenTokens[session.Token] = struct{}{}
	}

	if _, err = super.Exec(ctx, `UPDATE wde.console_sessions SET
		created_at=clock_timestamp()-interval '50 minutes',
		last_seen_at=clock_timestamp()-interval '2 minutes',
		idle_expires_at=clock_timestamp()+interval '1 minute',
		absolute_expires_at=clock_timestamp()+interval '5 minutes'
		WHERE id=$1`, created[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.Authenticate(ctx, created[0].Token); err != nil {
		t.Fatalf("sliding session rejected: %v", err)
	}
	var idleWithinAbsolute, touched bool
	if err = super.QueryRow(ctx, `SELECT idle_expires_at<=absolute_expires_at,
		last_seen_at>clock_timestamp()-interval '1 minute' FROM wde.console_sessions WHERE id=$1`, created[0].ID).
		Scan(&idleWithinAbsolute, &touched); err != nil || !idleWithinAbsolute || !touched {
		t.Fatalf("sliding idleWithinAbsolute=%v touched=%v err=%v", idleWithinAbsolute, touched, err)
	}

	if _, err = super.Exec(ctx, `UPDATE wde.console_sessions SET
		created_at=clock_timestamp()-interval '2 hours',last_seen_at=clock_timestamp()-interval '1 hour',
		idle_expires_at=clock_timestamp()-interval '30 minutes',absolute_expires_at=clock_timestamp()+interval '1 hour'
		WHERE id=$1`, created[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.Authenticate(ctx, created[1].Token); !errors.Is(err, consolesession.ErrUnauthorized) {
		t.Fatalf("idle-expired session authenticated: %v", err)
	}

	if _, err = super.Exec(ctx, `UPDATE wde.console_sessions SET
		created_at=clock_timestamp()-interval '2 hours',last_seen_at=clock_timestamp()-interval '61 minutes',
		idle_expires_at=clock_timestamp()-interval '1 second',absolute_expires_at=clock_timestamp()-interval '1 second'
		WHERE id=$1`, created[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = sessions.Authenticate(ctx, created[2].Token); !errors.Is(err, consolesession.ErrUnauthorized) {
		t.Fatalf("absolute-expired session authenticated: %v", err)
	}

	if err = sessions.Logout(ctx, created[3]); err != nil {
		t.Fatal(err)
	}
	if err = sessions.Logout(ctx, created[3]); err != nil {
		t.Fatalf("idempotent logout failed: %v", err)
	}
	if _, err = sessions.Authenticate(ctx, created[3].Token); !errors.Is(err, consolesession.ErrUnauthorized) {
		t.Fatalf("revoked session authenticated: %v", err)
	}
	var logoutAudits int
	if err = super.QueryRow(ctx, `SELECT count(*) FROM wde.audit_events
		WHERE workspace_id=$1 AND action='console.session.logout' AND resource_id=$2`,
		workspaceID, created[3].ID.String()).Scan(&logoutAudits); err != nil || logoutAudits != 1 {
		t.Fatalf("logout audits=%d err=%v", logoutAudits, err)
	}
}

func TestConsoleQueueSnapshotIncludesOldActiveDeliveries(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	consoleURL := os.Getenv("WDE_TEST_CONSOLE_DATABASE_URL")
	if consoleURL == "" {
		t.Skip("console integration database URL is not configured")
	}
	consoleDB := mustPool(t, ctx, consoleURL)
	defer consoleDB.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	materials, err := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	seeded := seedDeliveries(t, ctx, api, admin, materials, "console-old-queue", 1)
	if _, err = super.Exec(ctx, `UPDATE wde.deliveries SET
		created_at=clock_timestamp()-interval '48 hours',
		updated_at=clock_timestamp()-interval '48 hours',
		next_attempt_at=clock_timestamp()-interval '47 hours'
		WHERE workspace_id=$1`, seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := insights.NewPostgresStore(consoleDB).Snapshot(ctx, seeded.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Summary.Deliveries != 0 || snapshot.Summary.Pending != 1 ||
		snapshot.Summary.OldestReadySeconds < 46*60*60 {
		t.Fatalf("24h deliveries=%d queue pending=%d oldest=%f",
			snapshot.Summary.Deliveries, snapshot.Summary.Pending, snapshot.Summary.OldestReadySeconds)
	}
}

func TestConsoleMetricsQueryP95AtRepresentativeScale(t *testing.T) {
	ctx, api, admin, worker := reliabilityPools(t)
	defer api.Close()
	defer admin.Close()
	defer worker.Close()
	consoleURL := os.Getenv("WDE_TEST_CONSOLE_DATABASE_URL")
	if consoleURL == "" {
		t.Skip("console integration database URL is not configured")
	}
	consoleDB := mustPool(t, ctx, consoleURL)
	defer consoleDB.Close()
	super := mustPool(t, ctx, os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"))
	defer super.Close()
	materials, err := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	seeded := seedDeliveries(t, ctx, api, admin, materials, "console-metrics-scale", 1)
	var endpointID uuid.UUID
	if err = super.QueryRow(ctx, `SELECT id FROM wde.endpoints WHERE workspace_id=$1`, seeded.workspaceID).
		Scan(&endpointID); err != nil {
		t.Fatal(err)
	}
	const rows = 25_000
	if _, err = super.Exec(ctx, `INSERT INTO wde.events(
		id,workspace_id,idempotency_key_hash,idempotency_fingerprint,fingerprint_version,event_type,
		payload_cipher_format_version,payload_ciphertext,payload_nonce,payload_kek_version,payload_size,
		payload_expires_at,created_at)
		SELECT md5($1::text||'-event-'||value::text)::uuid,$1::uuid,
		decode(md5($1::text||'-key-a-'||value::text)||md5($1::text||'-key-b-'||value::text),'hex'),
		decode(md5($1::text||'-fp-a-'||value::text)||md5($1::text||'-fp-b-'||value::text),'hex'),
		1,'metrics.scale',1,decode(md5('payload-'||value::text),'hex'),
		decode(substr(md5('nonce-'||value::text),1,24),'hex'),1,16,
		clock_timestamp()+interval '1 day',clock_timestamp()-((value%3600)||' seconds')::interval
		FROM generate_series(1,$2) value`, seeded.workspaceID, rows); err != nil {
		t.Fatal(err)
	}
	if _, err = super.Exec(ctx, `INSERT INTO wde.deliveries(
		id,workspace_id,event_id,endpoint_id,status,succeeded_at,terminal_at,created_at,updated_at)
		SELECT md5($1::text||'-delivery-'||value::text)::uuid,$1::uuid,
		md5($1::text||'-event-'||value::text)::uuid,$2,'succeeded',clock_timestamp(),clock_timestamp(),
		clock_timestamp()-((value%3600)||' seconds')::interval,clock_timestamp()
		FROM generate_series(1,$3) value`, seeded.workspaceID, endpointID, rows); err != nil {
		t.Fatal(err)
	}
	if _, err = super.Exec(ctx, `INSERT INTO wde.delivery_attempts(
		id,workspace_id,delivery_id,run_number,attempt_number_in_run,attempt_sequence,fencing_token,
		state,outcome,http_status,duration_ms,response_bytes_read,started_at,finished_at)
		SELECT md5($1::text||'-attempt-'||value::text)::uuid,$1::uuid,
		md5($1::text||'-delivery-'||value::text)::uuid,1,1,1,1,'completed','success',204,
		(value%500),0,clock_timestamp()-((value%3600)||' seconds')::interval,
		clock_timestamp()-((value%3600)||' seconds')::interval
		FROM generate_series(1,$2) value`, seeded.workspaceID, rows); err != nil {
		t.Fatal(err)
	}
	if _, err = super.Exec(ctx, `ANALYZE wde.events; ANALYZE wde.deliveries; ANALYZE wde.delivery_attempts`); err != nil {
		t.Fatal(err)
	}
	store := insights.NewPostgresStore(consoleDB)
	if _, err = store.Snapshot(ctx, seeded.workspaceID); err != nil {
		t.Fatal(err)
	}
	durations := make([]time.Duration, 0, 5)
	for range 5 {
		started := time.Now()
		snapshot, snapshotErr := store.Snapshot(ctx, seeded.workspaceID)
		durations = append(durations, time.Since(started))
		if snapshotErr != nil || snapshot.Summary.Deliveries < rows || snapshot.Summary.Attempts < rows {
			t.Fatalf("snapshot deliveries=%d attempts=%d err=%v",
				snapshot.Summary.Deliveries, snapshot.Summary.Attempts, snapshotErr)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p95 := durations[len(durations)-1]
	t.Logf("tenant metrics p95=%s for %d attempts", p95, rows)
	if p95 >= 500*time.Millisecond {
		t.Fatalf("tenant metrics p95=%s, want <500ms for %d attempts", p95, rows)
	}
}
