// Package chaoslab provides a local-only webhook receiver for deterministic delivery tests.
package chaoslab

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

type state struct {
	mu      sync.RWMutex
	secrets map[string][]byte
}

// Handler returns the isolated Chaos Lab surface.
func Handler() http.Handler {
	state := &state{secrets: make(map[string][]byte)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	mux.HandleFunc("POST /configure", state.configure)
	mux.HandleFunc("POST /success", state.success)
	mux.Handle("/", problem.NotFound())
	return problem.WithRequestID(problem.Recover(mux))
}

func (s *state) configure(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	var input struct {
		KeyID  string `json:"key_id"`
		Secret string `json:"secret"`
	}
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF || len(input.KeyID) < 8 || len(input.KeyID) > 64 {
		problem.Write(w, r, http.StatusBadRequest, "invalid_configuration", "Invalid configuration")
		return
	}
	secret, err := base64.RawURLEncoding.Strict().DecodeString(input.Secret)
	if err != nil || len(secret) != 32 {
		clear(secret)
		problem.Write(w, r, http.StatusBadRequest, "invalid_configuration", "Invalid configuration")
		return
	}
	s.mu.Lock()
	if old := s.secrets[input.KeyID]; old != nil {
		clear(old)
	}
	s.secrets[input.KeyID] = secret
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *state) success(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(signing.HeaderVersion) != "v1" {
		problem.Write(w, r, http.StatusUnauthorized, "invalid_signature", "Invalid signature")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		problem.Write(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload too large")
		return
	}
	defer clear(body)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for keyID, secret := range s.secrets {
		if signing.Verify(secret, time.Now(), 5*time.Minute, r.Header.Get(signing.HeaderTimestamp), r.Header.Get(signing.HeaderEventID), r.Header.Get(signing.HeaderDeliveryID), keyID, r.Header.Get(signing.HeaderSignature), body) == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	problem.Write(w, r, http.StatusUnauthorized, "invalid_signature", "Invalid signature")
}
