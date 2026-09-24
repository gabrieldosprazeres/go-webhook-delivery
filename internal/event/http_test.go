package event

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
)

type errorStore struct {
	err   error
	calls int
}

func (s *errorStore) Publish(context.Context, NewRecord) (StoreResult, error) {
	s.calls++
	return StoreResult{}, s.err
}

func TestPublishReturnsIdempotencyConflictProblem(t *testing.T) {
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	store := &errorStore{err: ErrConflict}
	handler := NewHandler(NewService(store, materials))
	request := httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(`{"type":"x","data":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "same")
	response := httptest.NewRecorder()
	handler.Publish(response, request)
	if response.Code != http.StatusConflict || response.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(response.Body.String(), `"code":"idempotency_conflict"`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestPublishRejectsPayloadOverOneMiBBeforeStore(t *testing.T) {
	materials, _ := cryptobox.Load(config.ProfileTest, config.SecretFiles{})
	store := &errorStore{}
	handler := NewHandler(NewService(store, materials))
	request := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewReader(bytes.Repeat([]byte{'a'}, MaxPayloadBytes+1)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "large")
	response := httptest.NewRecorder()
	handler.Publish(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || store.calls != 0 {
		t.Fatalf("status=%d calls=%d", response.Code, store.calls)
	}
}
