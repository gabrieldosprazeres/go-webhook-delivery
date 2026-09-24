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

const (
	maxConfiguredSecrets   = 4
	maxConcurrentScenarios = 8
)

type state struct {
	mu              sync.RWMutex
	secrets         map[string][]byte
	counters        map[string]uint64
	lastScenario    string
	lastStatus      int
	validSignatures int
	slots           chan struct{}
	active, peak    int
}

// Handler returns the isolated Chaos Lab surface.
func Handler() http.Handler {
	return newHandler(&state{secrets: make(map[string][]byte), counters: make(map[string]uint64),
		slots: make(chan struct{}, maxConcurrentScenarios)})
}

func newHandler(state *state) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	mux.HandleFunc("POST /configure", state.configure)
	mux.Handle("POST /success", state.bounded(http.HandlerFunc(state.success)))
	mux.Handle("POST /verify-signature", state.bounded(http.HandlerFunc(state.verifySignature)))
	mux.Handle("POST /fail-n", state.bounded(http.HandlerFunc(state.failN)))
	mux.Handle("POST /timeout", state.bounded(http.HandlerFunc(state.timeout)))
	mux.Handle("POST /rate-limit", state.bounded(http.HandlerFunc(state.rateLimit)))
	mux.Handle("POST /permanent-failure", state.bounded(http.HandlerFunc(state.permanentFailure)))
	mux.HandleFunc("POST /reset", state.reset)
	mux.HandleFunc("GET /state", state.report)
	mux.Handle("/", problem.NotFound())
	return problem.WithRequestID(problem.Recover(mux))
}

func (s *state) bounded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case s.slots <- struct{}{}:
			s.mu.Lock()
			s.active++
			s.peak = max(s.peak, s.active)
			s.mu.Unlock()
			defer func() {
				s.mu.Lock()
				s.active--
				s.mu.Unlock()
				<-s.slots
			}()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			problem.Write(w, r, http.StatusTooManyRequests, "scenario_busy", "Scenario capacity reached")
		}
	})
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
	if _, exists := s.secrets[input.KeyID]; !exists && len(s.secrets) >= maxConfiguredSecrets {
		s.mu.Unlock()
		clear(secret)
		problem.Write(w, r, http.StatusConflict, "configuration_limit", "Configuration limit reached")
		return
	}
	if old := s.secrets[input.KeyID]; old != nil {
		clear(old)
	}
	s.secrets[input.KeyID] = secret
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *state) success(w http.ResponseWriter, r *http.Request) {
	s.verify(w, r, false, "success")
}

func (s *state) verifySignature(w http.ResponseWriter, r *http.Request) {
	s.verify(w, r, true, "verify_signature")
}

func (s *state) verify(w http.ResponseWriter, r *http.Request, requireAll bool, scenario string) {
	if r.Header.Get(signing.HeaderVersion) != "v1" {
		s.record(scenario, http.StatusUnauthorized, 0)
		problem.Write(w, r, http.StatusUnauthorized, "invalid_signature", "Invalid signature")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		s.record(scenario, http.StatusRequestEntityTooLarge, 0)
		problem.Write(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload too large")
		return
	}
	defer clear(body)
	s.mu.RLock()
	valid, configured := 0, len(s.secrets)
	for keyID, secret := range s.secrets {
		if signing.Verify(secret, time.Now(), 5*time.Minute, r.Header.Get(signing.HeaderTimestamp), r.Header.Get(signing.HeaderEventID), r.Header.Get(signing.HeaderDeliveryID), keyID, r.Header.Get(signing.HeaderSignature), body) == nil {
			valid++
		}
	}
	s.mu.RUnlock()
	if valid > 0 && (!requireAll || valid == configured) {
		s.record(scenario, http.StatusNoContent, valid)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.record(scenario, http.StatusUnauthorized, valid)
	problem.Write(w, r, http.StatusUnauthorized, "invalid_signature", "Invalid signature")
}
