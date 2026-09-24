// Package operational exposes minimal liveness and dynamic readiness probes.
package operational

import (
	"context"
	"net/http"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
)

// Handler returns a probe-only HTTP handler. Readiness is evaluated on every call.
func Handler(readiness func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := readiness(r.Context()); err != nil {
			problem.Write(w, r, http.StatusServiceUnavailable, "not_ready", "Service is not ready")
			return
		}
		writeStatus(w, "ready")
	})
	mux.Handle("/", problem.NotFound())
	return problem.WithRequestID(mux)
}

func writeStatus(w http.ResponseWriter, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"` + status + `"}` + "\n"))
}
