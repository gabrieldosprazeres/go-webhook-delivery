package integration

import (
	"context"
	"errors"
	"os"
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
