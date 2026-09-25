package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	webconsole "github.com/gabrieldosprazeres/go-webhook-delivery/internal/console"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/consolesession"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/insights"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/ratelimit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const consoleTestOrigin = "http://console.example.test"

type consoleHTTPFixture struct {
	t             *testing.T
	ctx           context.Context
	super, admin  *pgxpool.Pool
	consoleDB     *pgxpool.Pool
	materials     cryptobox.Materials
	sessions      *consolesession.Service
	handler       http.Handler
	server        *httptest.Server
	client        *http.Client
	workspaceID   uuid.UUID
	apiKey        string
	sessionCookie *http.Cookie
}

func TestConsoleHTTPWorkspaceJourneyIsolationAndQuota(t *testing.T) {
	fixture := newConsoleHTTPFixture(t)
	defer fixture.close()

	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	csrf := fixture.sessions.CSRF(fixture.sessionCookie.Value)
	created := fixture.postForm("/app/endpoints", fixture.sessionCookie, url.Values{
		"csrf_token": {csrf}, "url": {receiver.URL + "/hook"}, "event_types": {"console.e2e"},
	})
	assertConsoleStatus(t, created, http.StatusOK)
	var endpointID uuid.UUID
	if err := fixture.super.QueryRow(fixture.ctx, `SELECT id FROM wde.endpoints WHERE workspace_id=$1`,
		fixture.workspaceID).Scan(&endpointID); err != nil {
		t.Fatal(err)
	}

	published := fixture.postForm("/app/events", fixture.sessionCookie, url.Values{
		"csrf_token": {csrf}, "idempotency_key": {"console-http-event"},
		"payload": {`{"type":"console.e2e","data":{"ok":true}}`},
	})
	assertConsoleStatus(t, published, http.StatusOK)
	var deliveryID uuid.UUID
	if err := fixture.super.QueryRow(fixture.ctx, `SELECT id FROM wde.deliveries WHERE workspace_id=$1`,
		fixture.workspaceID).Scan(&deliveryID); err != nil {
		t.Fatal(err)
	}
	assertConsoleStatus(t, fixture.get("/app/endpoints/"+endpointID.String(), fixture.sessionCookie), http.StatusOK)
	assertConsoleStatus(t, fixture.get("/app/deliveries", fixture.sessionCookie), http.StatusOK)
	assertConsoleStatus(t, fixture.get("/app/deliveries/"+deliveryID.String(), fixture.sessionCookie), http.StatusOK)

	if _, err := fixture.super.Exec(fixture.ctx, `UPDATE wde.deliveries SET status='failed_permanent',
		terminal_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, deliveryID); err != nil {
		t.Fatal(err)
	}
	replayed := fixture.postForm("/app/deliveries/"+deliveryID.String()+"/replays", fixture.sessionCookie, url.Values{
		"csrf_token": {csrf}, "idempotency_key": {"console-http-replay"}, "reason": {"operator retry"},
	})
	assertConsoleStatus(t, replayed, http.StatusSeeOther)
	var replayCommands int
	if err := fixture.super.QueryRow(fixture.ctx, `SELECT count(*) FROM wde.replay_commands
		WHERE workspace_id=$1 AND delivery_id=$2`, fixture.workspaceID, deliveryID).Scan(&replayCommands); err != nil {
		t.Fatal(err)
	}
	if replayCommands != 1 {
		t.Fatalf("replay commands=%d", replayCommands)
	}

	otherWorkspace := uuid.New()
	if _, err := fixture.admin.Exec(fixture.ctx, `INSERT INTO wde.workspaces(id,name)
		VALUES($1,'console-http-other')`, otherWorkspace); err != nil {
		t.Fatal(err)
	}
	otherKey := insertConsoleCredential(t, fixture.ctx, fixture.admin, fixture.materials,
		otherWorkspace, []string{"deliveries:read", "deliveries:retry"})
	otherCookie := fixture.login(otherKey)
	assertConsoleStatus(t, fixture.get("/app/endpoints/"+endpointID.String(), otherCookie), http.StatusNotFound)
	crossReplay := fixture.postForm("/app/deliveries/"+deliveryID.String()+"/replays", otherCookie, url.Values{
		"csrf_token": {fixture.sessions.CSRF(otherCookie.Value)}, "idempotency_key": {"cross-tenant-replay"},
		"reason": {"must not cross tenant"},
	})
	assertConsoleStatus(t, crossReplay, http.StatusConflict)
	if err := fixture.super.QueryRow(fixture.ctx, `SELECT count(*) FROM wde.replay_commands
		WHERE delivery_id=$1`, deliveryID).Scan(&replayCommands); err != nil || replayCommands != 1 {
		t.Fatalf("cross-tenant replay commands=%d err=%v", replayCommands, err)
	}
	metricsResponse := fixture.get("/app/metrics", otherCookie)
	assertConsoleStatus(t, metricsResponse, http.StatusOK)
	var snapshot insights.Snapshot
	if err := json.NewDecoder(metricsResponse.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	_ = metricsResponse.Body.Close()
	if snapshot.Summary.Events != 0 || snapshot.Summary.Deliveries != 0 {
		t.Fatalf("cross-tenant metrics leaked: %+v", snapshot.Summary)
	}

	if _, err := fixture.super.Exec(fixture.ctx, `DELETE FROM wde.rate_limit_buckets`); err != nil {
		t.Fatal(err)
	}
	limited := fixture.newHandler(ratelimit.Policy{Operation: "query", Global: 1,
		Workspace: 1, APIKey: 1, Window: time.Minute})
	limitedServer := httptest.NewServer(limited)
	defer limitedServer.Close()
	first := consoleGET(t, fixture.client, limitedServer.URL+"/app/metrics", fixture.sessionCookie)
	assertConsoleStatus(t, first, http.StatusOK)
	second := consoleGET(t, fixture.client, limitedServer.URL+"/app/metrics", fixture.sessionCookie)
	assertConsoleStatus(t, second, http.StatusTooManyRequests)
}

func newConsoleHTTPFixture(t *testing.T) *consoleHTTPFixture {
	t.Helper()
	consoleURL, superURL, adminURL := os.Getenv("WDE_TEST_CONSOLE_DATABASE_URL"),
		os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL"), os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	if consoleURL == "" || superURL == "" || adminURL == "" {
		t.Skip("console HTTP integration database URLs are not configured")
	}
	ctx := context.Background()
	fixture := &consoleHTTPFixture{t: t, ctx: ctx, consoleDB: mustPool(t, ctx, consoleURL),
		super: mustPool(t, ctx, superURL), admin: mustPool(t, ctx, adminURL)}
	var err error
	fixture.materials, err = cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessions = consolesession.New(consolesession.NewPostgresStore(fixture.consoleDB),
		auth.NewAuthenticator(auth.NewPostgresLookup(fixture.consoleDB), fixture.materials.AuthPepper),
		fixture.materials.SessionPepper, fixture.materials.CSRFPepper)
	fixture.handler = fixture.newHandler(ratelimit.Policy{Operation: "query", Global: 1000,
		Workspace: 1000, APIKey: 1000, Window: time.Minute})
	fixture.server = httptest.NewServer(fixture.handler)
	fixture.client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	fixture.workspaceID = uuid.New()
	if _, err = fixture.admin.Exec(ctx, `INSERT INTO wde.workspaces(id,name)
		VALUES($1,'console-http-e2e')`, fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	fixture.apiKey = insertConsoleCredential(t, ctx, fixture.admin, fixture.materials, fixture.workspaceID,
		[]string{"deliveries:read", "deliveries:retry", "endpoints:write", "events:write"})
	fixture.sessionCookie = fixture.login(fixture.apiKey)
	return fixture
}

func (f *consoleHTTPFixture) newHandler(queryPolicy ratelimit.Policy) http.Handler {
	endpointStore := endpoint.NewPostgresStore(f.consoleDB)
	deliveryStore := delivery.NewPostgresStore(f.consoleDB, f.materials.CursorPepper)
	ingestPolicy := ratelimit.Policy{Operation: "ingest", Global: 1000,
		Workspace: 1000, APIKey: 1000, Window: time.Minute}
	endpointPolicy := ratelimit.Policy{Operation: "endpoint_write", Global: 1000,
		Workspace: 1000, APIKey: 1000, Window: time.Minute}
	return webconsole.New(webconsole.Dependencies{
		Sessions:       f.sessions,
		Endpoints:      endpoint.NewService(endpointStore, config.ProfileTest, true, f.materials),
		EndpointLister: endpoint.NewLister(endpointStore, f.materials),
		Events:         event.NewService(event.NewPostgresStore(f.consoleDB), f.materials),
		Deliveries:     deliveryStore,
		Replay:         delivery.NewReplayService(deliveryStore, f.materials),
		Insights:       insights.NewPostgresStore(f.consoleDB),
		Origin:         consoleTestOrigin,
		Limiter:        ratelimit.New(f.consoleDB, f.materials.RateLimitPepper),
		QueryPolicy:    queryPolicy, IngestPolicy: ingestPolicy, EndpointPolicy: endpointPolicy,
		ReplayPolicy: ratelimit.Policy{Operation: "replay", Global: 1000,
			Workspace: 1000, APIKey: 1000, Resource: 1000, Window: time.Minute},
	}).Handler()
}

func (f *consoleHTTPFixture) login(apiKey string) *http.Cookie {
	f.t.Helper()
	page := consoleGET(f.t, f.client, f.server.URL+"/login", nil)
	assertConsoleStatus(f.t, page, http.StatusOK)
	var csrfCookie *http.Cookie
	for _, cookie := range page.Cookies() {
		if cookie.Name == "wde_login_csrf" {
			csrfCookie = cookie
		}
	}
	_ = page.Body.Close()
	if csrfCookie == nil {
		f.t.Fatal("login CSRF cookie missing")
	}
	form := url.Values{"csrf_token": {csrfCookie.Value}, "api_key": {apiKey}}
	request, err := http.NewRequest(http.MethodPost, f.server.URL+"/session", strings.NewReader(form.Encode()))
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Origin", consoleTestOrigin)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(csrfCookie)
	response, err := f.client.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	assertConsoleStatus(f.t, response, http.StatusSeeOther)
	var sessionCookie *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == "wde_session" {
			sessionCookie = cookie
		}
	}
	_ = response.Body.Close()
	if sessionCookie == nil {
		f.t.Fatal("session cookie missing")
	}
	return sessionCookie
}

func (f *consoleHTTPFixture) get(path string, cookie *http.Cookie) *http.Response {
	f.t.Helper()
	return consoleGET(f.t, f.client, f.server.URL+path, cookie)
}

func (f *consoleHTTPFixture) postForm(path string, cookie *http.Cookie, values url.Values) *http.Response {
	f.t.Helper()
	request, err := http.NewRequest(http.MethodPost, f.server.URL+path, strings.NewReader(values.Encode()))
	if err != nil {
		f.t.Fatal(err)
	}
	request.Header.Set("Origin", consoleTestOrigin)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.AddCookie(cookie)
	response, err := f.client.Do(request)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

func (f *consoleHTTPFixture) close() {
	f.server.Close()
	f.consoleDB.Close()
	f.admin.Close()
	f.super.Close()
}

func consoleGET(t *testing.T, client *http.Client, target string, cookie *http.Cookie) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertConsoleStatus(t *testing.T, response *http.Response, status int) {
	t.Helper()
	if response.StatusCode == status {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	_ = response.Body.Close()
	t.Fatalf("status=%d want=%d body=%s", response.StatusCode, status, body)
}

func insertConsoleCredential(t *testing.T, ctx context.Context, admin *pgxpool.Pool,
	materials cryptobox.Materials, workspaceID uuid.UUID, scopes []string,
) string {
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
	for _, scope := range scopes {
		if _, err = admin.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope)
			VALUES($1,$2,$3)`, workspaceID, keyID, scope); err != nil {
			t.Fatal(err)
		}
	}
	return token
}
