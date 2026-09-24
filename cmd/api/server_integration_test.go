package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pipelineFixture struct {
	victimToken, attackerToken, limitedToken string
	victimWorkspace, attackerWorkspace       uuid.UUID
	deliveryID                               uuid.UUID
}

func TestProductionRoutePipelineBoundsAuthScopeAndCrossTenantQuota(t *testing.T) {
	ctx, api, admin, super := pipelinePools(t)
	defer api.Close()
	defer admin.Close()
	defer super.Close()
	materials, err := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	if err != nil {
		t.Fatal(err)
	}
	fixture := seedPipelineFixture(t, ctx, admin, super, materials)

	t.Run("invalid credential is locally bounded before repeated lookup", func(t *testing.T) {
		cfg := pipelineConfig(t, os.Getenv("WDE_TEST_API_DATABASE_URL"))
		cfg.Edge.Origin = 1
		server := httptest.NewServer(newPublicServer(cfg, api, materials).Handler)
		defer server.Close()
		unknown, _, _, err := auth.Generate(true, materials.AuthPepper)
		if err != nil {
			t.Fatal(err)
		}
		first := pipelineRequest(t, server.URL+"/v1/deliveries", unknown, "", "")
		assertPipelineStatus(t, first, http.StatusUnauthorized, "unauthorized")
		request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/deliveries", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+unknown)
		request.Header.Set("X-Forwarded-For", "198.51.100.250")
		second, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		assertPipelineStatus(t, second, http.StatusTooManyRequests, "quota_exceeded")
	})

	t.Run("authenticated quota precedes scope authorization", func(t *testing.T) {
		clearPipelineBuckets(t, ctx, super)
		server := httptest.NewServer(newPublicServer(pipelineConfig(t,
			os.Getenv("WDE_TEST_API_DATABASE_URL")), api, materials).Handler)
		defer server.Close()
		url := server.URL + "/v1/deliveries/" + fixture.deliveryID.String() + "/replays"
		first := pipelineRequest(t, url, fixture.limitedToken, "scope-quota", `{"reason":"retry"}`)
		assertPipelineStatus(t, first, http.StatusForbidden, "insufficient_scope")
		second := pipelineRequest(t, url, fixture.limitedToken, "scope-quota-2", `{"reason":"retry"}`)
		assertPipelineStatus(t, second, http.StatusTooManyRequests, "quota_exceeded")
	})

	t.Run("attacker cannot poison victim delivery bucket", func(t *testing.T) {
		clearPipelineBuckets(t, ctx, super)
		server := httptest.NewServer(newPublicServer(pipelineConfig(t,
			os.Getenv("WDE_TEST_API_DATABASE_URL")), api, materials).Handler)
		defer server.Close()
		url := server.URL + "/v1/deliveries/" + fixture.deliveryID.String() + "/replays"
		first := pipelineRequest(t, url, fixture.attackerToken, "attacker-one", `{"reason":"retry"}`)
		assertPipelineStatus(t, first, http.StatusNotFound, "not_found")
		second := pipelineRequest(t, url, fixture.attackerToken, "attacker-two", `{"reason":"retry"}`)
		assertPipelineStatus(t, second, http.StatusTooManyRequests, "quota_exceeded")
		victim := pipelineRequest(t, url, fixture.victimToken, "victim-one", `{"reason":"retry"}`)
		defer victim.Body.Close()
		if victim.StatusCode != http.StatusAccepted {
			t.Fatalf("victim status=%d body=%s", victim.StatusCode, readPipelineBody(t, victim))
		}
		var result delivery.ReplayResult
		if err := json.NewDecoder(victim.Body).Decode(&result); err != nil || result.DeliveryID != fixture.deliveryID {
			t.Fatalf("victim result=%+v err=%v", result, err)
		}
		var buckets, workspaces, hashes int
		if err := super.QueryRow(ctx, `SELECT count(*),count(DISTINCT workspace_id),
			count(DISTINCT encode(dimension_hash,'hex')) FROM wde.rate_limit_buckets
			WHERE operation='replay' AND dimension_type='delivery' AND resource_id=$1`, fixture.deliveryID).
			Scan(&buckets, &workspaces, &hashes); err != nil {
			t.Fatal(err)
		}
		if buckets != 2 || workspaces != 2 || hashes != 2 {
			t.Fatalf("delivery buckets=%d workspaces=%d hashes=%d", buckets, workspaces, hashes)
		}
	})

	t.Run("cursor is tenant bound and tamper evident", func(t *testing.T) {
		clearPipelineBuckets(t, ctx, super)
		server := httptest.NewServer(newPublicServer(pipelineConfig(t,
			os.Getenv("WDE_TEST_API_DATABASE_URL")), api, materials).Handler)
		defer server.Close()
		page := pipelineRequest(t, server.URL+"/v1/deliveries?limit=1", fixture.victimToken, "", "")
		defer page.Body.Close()
		var result delivery.ListResult
		if page.StatusCode != http.StatusOK || json.NewDecoder(page.Body).Decode(&result) != nil || result.NextCursor == "" {
			t.Fatalf("page status=%d result=%+v", page.StatusCode, result)
		}
		crossTenant := pipelineRequest(t, server.URL+"/v1/deliveries?limit=1&cursor="+result.NextCursor,
			fixture.attackerToken, "", "")
		assertPipelineStatus(t, crossTenant, http.StatusBadRequest, "invalid_page")
		tampered := []byte(result.NextCursor)
		tampered[len(tampered)-1] ^= 1
		invalid := pipelineRequest(t, server.URL+"/v1/deliveries?limit=1&cursor="+string(tampered),
			fixture.victimToken, "", "")
		assertPipelineStatus(t, invalid, http.StatusBadRequest, "invalid_page")
	})
}

func pipelineConfig(t *testing.T, databaseURL string) config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{Service: config.ServiceAPI, LookupEnv: func(name string) (string, bool) {
		values := map[string]string{"WDE_PROFILE": "test", "WDE_DATABASE_URL": databaseURL}
		value, ok := values[name]
		return value, ok
	}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Edge = config.EdgeConfig{MaxInFlight: 32, Global: 1000, Origin: 1000, Prefix: 1000,
		MaxBuckets: 64, Window: time.Minute}
	cfg.Quotas.Replay = config.QuotaPolicy{Global: 100, Workspace: 100, APIKey: 100,
		Resource: 1, Window: time.Minute}
	return cfg
}

func pipelinePools(t *testing.T) (context.Context, *pgxpool.Pool, *pgxpool.Pool, *pgxpool.Pool) {
	t.Helper()
	apiURL := os.Getenv("WDE_TEST_API_DATABASE_URL")
	adminURL := os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	superURL := os.Getenv("WDE_TEST_SUPERUSER_DATABASE_URL")
	if apiURL == "" || adminURL == "" || superURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	return ctx, pipelinePool(t, ctx, apiURL), pipelinePool(t, ctx, adminURL), pipelinePool(t, ctx, superURL)
}

func pipelinePool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func pipelineRequest(t *testing.T, url, token, idempotencyKey, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Method = http.MethodPost
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertPipelineStatus(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	body := readPipelineBody(t, response)
	if response.StatusCode != status || !bytes.Contains(body, []byte(`"code":"`+code+`"`)) {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
}

func readPipelineBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return body
}
