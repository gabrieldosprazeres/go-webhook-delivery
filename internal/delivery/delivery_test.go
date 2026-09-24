package delivery

import (
	"context"
	"encoding/base64"
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
	success   bool
}

func (s *runnerStore) Claim(context.Context, uuid.UUID, uuid.UUID, time.Duration) (Claim, bool, error) {
	if s.claimed {
		return Claim{}, false, nil
	}
	s.claimed = true
	return s.claim, true, nil
}
func (s *runnerStore) Finalize(_ context.Context, _ Claim, _ uuid.UUID, success bool, _ *int16, _ int, _ string) (bool, error) {
	s.finalized = true
	s.success = success
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
	if !worked || !store.finalized || !store.success {
		t.Fatalf("worked=%v finalized=%v success=%v", worked, store.finalized, store.success)
	}
}

func TestHTTPDeliveryRequiresExplicitFlag(t *testing.T) {
	store := &runnerStore{claim: Claim{Scheme: "http"}}
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	runner := NewRunner(store, materials, config.ProfileTest, false, uuid.New())
	worked, err := runner.ProcessOne(context.Background())
	if err != nil || !worked || !store.finalized || store.success {
		t.Fatalf("worked=%v finalized=%v success=%v err=%v", worked, store.finalized, store.success, err)
	}
}
