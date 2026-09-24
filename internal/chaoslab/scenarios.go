package chaoslab

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
)

func (s *state) failN(w http.ResponseWriter, r *http.Request) {
	failures, ok := boundedQueryInt(r, "failures", 2, 1, 10)
	if !ok {
		problem.Write(w, r, http.StatusBadRequest, "invalid_scenario", "Invalid scenario")
		return
	}
	call := s.increment("fail_n_then_succeed")
	if call <= uint64(failures) {
		s.recordStatus("fail_n_then_succeed", http.StatusServiceUnavailable)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	s.recordStatus("fail_n_then_succeed", http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}

func (s *state) timeout(w http.ResponseWriter, r *http.Request) {
	delayMS, ok := boundedQueryInt(r, "delay_ms", 11000, 100, 20000)
	if !ok {
		problem.Write(w, r, http.StatusBadRequest, "invalid_scenario", "Invalid scenario")
		return
	}
	s.increment("timeout")
	timer := time.NewTimer(time.Duration(delayMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		return
	case <-timer.C:
		s.recordStatus("timeout", http.StatusNoContent)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *state) rateLimit(w http.ResponseWriter, r *http.Request) {
	retryAfter, ok := boundedQueryInt(r, "retry_after", 1, 1, 900)
	if !ok {
		problem.Write(w, r, http.StatusBadRequest, "invalid_scenario", "Invalid scenario")
		return
	}
	s.increment("rate_limit")
	s.recordStatus("rate_limit", http.StatusTooManyRequests)
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)
}

func (s *state) permanentFailure(w http.ResponseWriter, _ *http.Request) {
	s.increment("permanent_failure")
	s.recordStatus("permanent_failure", http.StatusBadRequest)
	w.WriteHeader(http.StatusBadRequest)
}

func (s *state) reset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.counters = make(map[string]uint64)
	s.lastScenario, s.lastStatus, s.validSignatures = "", 0, 0
	s.peak = s.active
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *state) report(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	response := struct {
		Counters        map[string]uint64 `json:"counters"`
		LastScenario    string            `json:"last_scenario"`
		LastStatus      int               `json:"last_status"`
		ValidSignatures int               `json:"valid_signatures"`
		Active          int               `json:"active"`
		Peak            int               `json:"peak"`
	}{make(map[string]uint64, len(s.counters)), s.lastScenario, s.lastStatus, s.validSignatures,
		s.active, s.peak}
	for scenario, count := range s.counters {
		response.Counters[scenario] = count
	}
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(response)
}

func boundedQueryInt(r *http.Request, name string, fallback, minimum, maximum int) (int, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if key != name || len(values) != 1 {
			return 0, false
		}
	}
	if query.Get(name) == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(query.Get(name))
	return value, err == nil && value >= minimum && value <= maximum
}
