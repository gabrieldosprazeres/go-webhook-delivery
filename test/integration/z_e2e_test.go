package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/chaoslab"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/delivery"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/endpoint"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/event"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/google/uuid"
)

func TestEndpointEventWorkerChaosLabSucceeded(t *testing.T) {
	apiURL := os.Getenv("WDE_TEST_API_DATABASE_URL")
	adminURL := os.Getenv("WDE_TEST_ADMIN_DATABASE_URL")
	workerURL := os.Getenv("WDE_TEST_WORKER_DATABASE_URL")
	if apiURL == "" || adminURL == "" || workerURL == "" {
		t.Skip("integration database URLs are not configured")
	}
	ctx := context.Background()
	apiPool := mustPool(t, ctx, apiURL)
	defer apiPool.Close()
	adminPool := mustPool(t, ctx, adminURL)
	defer adminPool.Close()
	workerPool := mustPool(t, ctx, workerURL)
	defer workerPool.Close()
	suspendActiveWorkspaces(t, ctx, adminPool)
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	workspaceID, otherWorkspaceID := uuid.New(), uuid.New()
	keyID, otherKeyID, limitedKeyID := uuid.New(), uuid.New(), uuid.New()
	token, prefix, verifier, err := auth.Generate(true, materials.AuthPepper)
	if err != nil {
		t.Fatal(err)
	}
	otherToken, otherPrefix, otherVerifier, err := auth.Generate(true, materials.AuthPepper)
	if err != nil {
		t.Fatal(err)
	}
	limitedToken, limitedPrefix, limitedVerifier, err := auth.Generate(true, materials.AuthPepper)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := adminPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'e2e')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.workspaces(id,name) VALUES($1,'e2e-other')`, otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier) VALUES($1,$2,$3,$4)`, keyID, workspaceID, prefix, verifier[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier) VALUES($1,$2,$3,$4)`, otherKeyID, otherWorkspaceID, otherPrefix, otherVerifier[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wde.api_keys(id,workspace_id,prefix,verifier) VALUES($1,$2,$3,$4)`, limitedKeyID, workspaceID, limitedPrefix, limitedVerifier[:]); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"deliveries:read", "endpoints:write", "events:write"} {
		if _, err = tx.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope) VALUES($1,$2,$3)`, workspaceID, keyID, scope); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []struct {
		workspaceID uuid.UUID
		keyID       uuid.UUID
	}{{otherWorkspaceID, otherKeyID}, {workspaceID, limitedKeyID}} {
		if _, err = tx.Exec(ctx, `INSERT INTO wde.api_key_scopes(workspace_id,api_key_id,scope) VALUES($1,$2,'deliveries:read')`, key.workspaceID, key.keyID); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	authenticator := auth.NewAuthenticator(auth.NewPostgresLookup(apiPool), materials.AuthPepper)
	endpointHandler := endpoint.NewHandler(endpoint.NewService(endpoint.NewPostgresStore(apiPool), config.ProfileTest, true, materials))
	eventHandler := event.NewHandler(event.NewService(event.NewPostgresStore(apiPool), materials))
	deliveryStore := delivery.NewPostgresStore(apiPool)
	deliveryHandler := delivery.NewHandler(deliveryStore)
	mux := http.NewServeMux()
	mux.Handle("POST /v1/endpoints", authenticator.Middleware("endpoints:write", http.HandlerFunc(endpointHandler.Create)))
	mux.Handle("GET /v1/endpoints/{id}", authenticator.Middleware("deliveries:read", http.HandlerFunc(endpointHandler.Get)))
	mux.Handle("POST /v1/events", authenticator.Middleware("events:write", http.HandlerFunc(eventHandler.Publish)))
	mux.Handle("GET /v1/deliveries/{id}", authenticator.Middleware("deliveries:read", http.HandlerFunc(deliveryHandler.Get)))
	apiServer := httptest.NewServer(problem.WithRequestID(problem.Recover(mux)))
	defer apiServer.Close()
	chaosServer := httptest.NewServer(chaoslab.Handler())
	defer chaosServer.Close()
	forbiddenResponse := authorizedJSON(t, http.MethodPost, apiServer.URL+"/v1/endpoints", limitedToken, "", fmt.Sprintf(`{"url":%q,"event_types":["invoice.created"]}`, chaosServer.URL+"/success"))
	assertProblem(t, forbiddenResponse, http.StatusForbidden, "insufficient_scope")
	createdResponse := authorizedJSON(t, http.MethodPost, apiServer.URL+"/v1/endpoints", token, "", fmt.Sprintf(`{"url":%q,"event_types":["invoice.created"]}`, chaosServer.URL+"/success"))
	defer createdResponse.Body.Close()
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("endpoint status=%d", createdResponse.StatusCode)
	}
	var created endpoint.Created
	if err = json.NewDecoder(createdResponse.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	endpointResponse := authorizedJSON(t, http.MethodGet, apiServer.URL+"/v1/endpoints/"+created.Endpoint.ID.String(), token, "", "")
	endpointBody := readAndClose(t, endpointResponse)
	if endpointResponse.StatusCode != http.StatusOK || !bytes.Contains(endpointBody, []byte(created.Endpoint.ID.String())) {
		t.Fatalf("endpoint status=%d body=%s", endpointResponse.StatusCode, endpointBody)
	}
	if bytes.Contains(endpointBody, []byte(created.SigningSecret.Secret)) || bytes.Contains(endpointBody, []byte("signing_secret")) || bytes.Contains(endpointBody, []byte("ciphertext")) {
		t.Fatalf("endpoint lookup exposed secret material: %s", endpointBody)
	}
	crossTenantResponse := authorizedJSON(t, http.MethodGet, apiServer.URL+"/v1/endpoints/"+created.Endpoint.ID.String(), otherToken, "", "")
	assertProblem(t, crossTenantResponse, http.StatusNotFound, "not_found")
	configured := postJSON(t, chaosServer.URL+"/configure", fmt.Sprintf(`{"key_id":%q,"secret":%q}`, created.SigningSecret.KeyID, created.SigningSecret.Secret))
	configured.Body.Close()
	if configured.StatusCode != http.StatusNoContent {
		t.Fatalf("configure status=%d", configured.StatusCode)
	}
	publishedResponse := authorizedJSON(t, http.MethodPost, apiServer.URL+"/v1/events", token, "e2e-key", `{"type":"invoice.created","data":{"amount":1999}}`)
	defer publishedResponse.Body.Close()
	if publishedResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("event status=%d", publishedResponse.StatusCode)
	}
	var published event.PublishResult
	if err = json.NewDecoder(publishedResponse.Body).Decode(&published); err != nil {
		t.Fatal(err)
	}
	if len(published.DeliveryIDs) != 1 {
		t.Fatalf("deliveries=%v", published.DeliveryIDs)
	}
	duplicateResponse := authorizedJSON(t, http.MethodPost, apiServer.URL+"/v1/events", token, "e2e-key", `{"data":{"amount":1999},"type":"invoice.created"}`)
	defer duplicateResponse.Body.Close()
	if duplicateResponse.StatusCode != http.StatusAccepted {
		t.Fatalf("duplicate status=%d", duplicateResponse.StatusCode)
	}
	var duplicate event.PublishResult
	if err = json.NewDecoder(duplicateResponse.Body).Decode(&duplicate); err != nil {
		t.Fatal(err)
	}
	if duplicate.EventID != published.EventID || len(duplicate.DeliveryIDs) != 1 || duplicate.DeliveryIDs[0] != published.DeliveryIDs[0] || !duplicate.Duplicate {
		t.Fatalf("duplicate=%+v published=%+v", duplicate, published)
	}
	conflictResponse := authorizedJSON(t, http.MethodPost, apiServer.URL+"/v1/events", token, "e2e-key", `{"type":"invoice.created","data":{"amount":2000}}`)
	assertProblem(t, conflictResponse, http.StatusConflict, "idempotency_conflict")
	largeResponse := authorizedJSON(t, http.MethodPost, apiServer.URL+"/v1/events", token, "large-key", strings.Repeat("a", event.MaxPayloadBytes+1))
	assertProblem(t, largeResponse, http.StatusRequestEntityTooLarge, "payload_too_large")
	runner := delivery.NewRunner(delivery.NewPostgresStore(workerPool), materials, config.ProfileTest, true, uuid.New())
	succeeded := false
	for range 10 {
		worked, err := runner.ProcessOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
		detailsResponse := authorizedJSON(t, http.MethodGet, apiServer.URL+"/v1/deliveries/"+published.DeliveryIDs[0].String(), token, "", "")
		detailsBody := readAndClose(t, detailsResponse)
		for _, forbidden := range []string{created.SigningSecret.Secret, "payload", "ciphertext", "secret"} {
			if strings.Contains(string(detailsBody), forbidden) {
				t.Fatalf("delivery timeline exposed %q: %s", forbidden, detailsBody)
			}
		}
		var details delivery.Details
		if err = json.Unmarshal(detailsBody, &details); err != nil {
			t.Fatal(err)
		}
		if details.Status == "succeeded" {
			if len(details.Attempts) != 1 || details.Attempts[0].Outcome == nil || *details.Attempts[0].Outcome != "success" {
				t.Fatalf("attempts=%+v", details.Attempts)
			}
			succeeded = true
			break
		}
	}
	if !succeeded {
		t.Fatal("delivery did not reach succeeded")
	}
	for _, status := range []string{"suspended", "deleting"} {
		if _, err = adminPool.Exec(ctx, `UPDATE wde.workspaces SET status=$2,updated_at=clock_timestamp() WHERE id=$1`, workspaceID, status); err != nil {
			t.Fatal(err)
		}
		inactiveResponse := authorizedJSON(t, http.MethodGet, apiServer.URL+"/v1/endpoints/"+created.Endpoint.ID.String(), token, "", "")
		assertProblem(t, inactiveResponse, http.StatusUnauthorized, "unauthorized")
	}
}

func authorizedJSON(t *testing.T, method, url, token, idempotencyKey, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	response, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertProblem(t *testing.T, response *http.Response, status int, code string) {
	t.Helper()
	body := readAndClose(t, response)
	if response.StatusCode != status || response.Header.Get("Content-Type") != "application/problem+json" || !bytes.Contains(body, []byte(`"code":"`+code+`"`)) {
		t.Fatalf("status=%d content-type=%q body=%s", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
}

func readAndClose(t *testing.T, response *http.Response) []byte {
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
