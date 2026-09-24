package delivery

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
	"github.com/google/uuid"
)

type runnerStore struct {
	claim     Claim
	claimed   bool
	finalized bool
	result    Result
}

func (s *runnerStore) ClaimBatch(_ context.Context, request ClaimRequest) ([]Claim, error) {
	if s.claimed {
		return nil, nil
	}
	s.claimed = true
	return []Claim{s.claim}, nil
}
func (s *runnerStore) Finalize(_ context.Context, _ Claim, _ uuid.UUID, result Result) (bool, error) {
	s.finalized = true
	s.result = result
	return true, nil
}
func (*runnerStore) Get(context.Context, uuid.UUID, uuid.UUID) (Details, error) {
	return Details{}, ErrNotFound
}

func TestProcessOneSignsRawBodyAndFinalizesSuccess(t *testing.T) {
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 1)
	}
	payload := []byte(`{"type":"invoice.created","data":{"amount":1999}}`)
	workspaceID, eventID, deliveryID, endpointID, secretID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	keyID := "key_testvector"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded, _ := base64.RawURLEncoding.DecodeString(base64.RawURLEncoding.EncodeToString(secret))
		if string(body) != string(payload) {
			t.Errorf("body=%s", body)
		}
		if err := signing.Verify(decoded, time.Now(), 5*time.Minute, r.Header.Get(signing.HeaderTimestamp), eventID.String(), deliveryID.String(), keyID, r.Header.Get(signing.HeaderSignature), body); err != nil {
			t.Errorf("verify: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(parsed.Port())
	target, _ := cryptobox.Seal(materials.Signing, []byte("/"), cryptobox.AAD("1", workspaceID.String(), endpointID.String(), "http", parsed.Hostname(), strconv.Itoa(port)))
	payloadEnvelope, _ := cryptobox.Seal(materials.Payload, payload, cryptobox.AAD("1", workspaceID.String(), eventID.String(), "invoice.created"))
	secretEnvelope, _ := cryptobox.Seal(materials.Signing, secret, cryptobox.AAD("1", workspaceID.String(), endpointID.String(), secretID.String(), keyID))
	store := &runnerStore{claim: Claim{WorkspaceID: workspaceID, EventID: eventID, DeliveryID: deliveryID, EndpointID: endpointID, FencingToken: 1, Scheme: "http", Host: parsed.Hostname(), Port: port, Target: target, EventType: "invoice.created", Payload: payloadEnvelope, KeyID: keyID, SecretVersionID: secretID, Secret: secretEnvelope}}
	runner := NewRunner(store, materials, config.ProfileTest, true, uuid.New())
	worked, err := runner.ProcessOne(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !worked || !store.finalized || store.result.Disposition != DispositionSuccess {
		t.Fatalf("worked=%v finalized=%v result=%+v", worked, store.finalized, store.result)
	}
}

func TestHTTPDeliveryRequiresExplicitFlag(t *testing.T) {
	store := &runnerStore{claim: Claim{Scheme: "http"}}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	runner := NewRunner(store, materials, config.ProfileTest, false, uuid.New())
	worked, err := runner.ProcessOne(context.Background())
	if err != nil || !worked || !store.finalized || store.result.Disposition != DispositionPermanent {
		t.Fatalf("worked=%v finalized=%v result=%+v err=%v", worked, store.finalized, store.result, err)
	}
}

func TestClaimRequestValidationHappensBeforeAllocation(t *testing.T) {
	if _, err := NewPostgresStore(nil).ClaimBatch(context.Background(), ClaimRequest{}); !errors.Is(err, ErrInvalidClaimRequest) {
		t.Fatalf("invalid request reached store allocation: %v", err)
	}
	valid := ClaimRequest{WorkerID: uuid.New(), LeaseTTL: 20 * time.Second, Limit: 2, WorkspaceLimit: 1, EndpointLimit: 1}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	invalid := []ClaimRequest{
		{},
		{WorkerID: uuid.New(), LeaseTTL: 19 * time.Second, Limit: 1, WorkspaceLimit: 1, EndpointLimit: 1},
		{WorkerID: uuid.New(), LeaseTTL: 20 * time.Second, Limit: 101, WorkspaceLimit: 1, EndpointLimit: 1},
		{WorkerID: uuid.New(), LeaseTTL: 20 * time.Second, Limit: 1, WorkspaceLimit: 2, EndpointLimit: 1},
		{WorkerID: uuid.New(), LeaseTTL: 20 * time.Second, Limit: 1, WorkspaceLimit: 1, EndpointLimit: 2},
	}
	for index, request := range invalid {
		if err := request.Validate(); !errors.Is(err, ErrInvalidClaimRequest) {
			t.Fatalf("request[%d] err=%v", index, err)
		}
	}
}

func TestNormalizeResultRejectsOutOfRangeStatus(t *testing.T) {
	status := int16(699)
	result := normalizeResult(Result{Disposition: DispositionRetry, HTTPStatus: &status, Category: "http_retryable", RetryAfter: time.Second})
	if result.Disposition != DispositionPermanent || result.HTTPStatus != nil ||
		result.Category != "invalid_http_status" || result.RetryAfter != 0 {
		t.Fatalf("result=%+v", result)
	}
}
