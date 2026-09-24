package event

import (
	"context"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type eventMemoryStore struct{ record NewRecord }

func (m *eventMemoryStore) Publish(_ context.Context, r NewRecord) (StoreResult, error) {
	m.record = r
	return StoreResult{EventID: r.ID}, nil
}
func TestPublishEncryptsRawPayload(t *testing.T) {
	mats, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	store := &eventMemoryStore{}
	service := NewService(store, mats)
	raw := []byte(`{"data":{"total":10},"type":"invoice.created"}`)
	result, err := service.Publish(context.Background(), uuid.New(), "key-1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if result.EventID == uuid.Nil {
		t.Fatal("missing id")
	}
	plain, err := cryptobox.Open(mats.Payload, store.record.Payload, cryptobox.AAD("2", store.record.WorkspaceID.String(), "event_payload", store.record.ID.String(), store.record.EventType))
	if err != nil || string(plain) != string(raw) {
		t.Fatalf("plain=%s err=%v", plain, err)
	}
}
func TestCanonicalFingerprintIgnoresObjectWhitespace(t *testing.T) {
	_, a, err := validateAndCanonicalize([]byte(`{"type":"x","data":{"a":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := validateAndCanonicalize([]byte("{ \"data\": {\"a\":1}, \"type\": \"x\" }"))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("%s != %s", a, b)
	}
}

func TestCanonicalFingerprintNormalizesNumbersAndRejectsDuplicateKeys(t *testing.T) {
	_, a, err := validateAndCanonicalize([]byte(`{"type":"x","data":{"amount":1.0}}`))
	if err != nil {
		t.Fatal(err)
	}
	_, b, err := validateAndCanonicalize([]byte(`{"data":{"amount":1},"type":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("%s != %s", a, b)
	}
	if _, _, err := validateAndCanonicalize([]byte(`{"type":"x","type":"y","data":{}}`)); err == nil {
		t.Fatal("duplicate key accepted")
	}
}
