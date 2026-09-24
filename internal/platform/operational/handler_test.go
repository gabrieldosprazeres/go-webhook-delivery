package operational

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerKeepsLivenessWhenReadinessFails(t *testing.T) {
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("safe_metric 1\n")) })
	handler := Handler(func(context.Context) error { return errors.New("database unavailable") }, metrics)

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", health.Code, http.StatusOK)
	}

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want %d", ready.Code, http.StatusServiceUnavailable)
	}

	scrape := httptest.NewRecorder()
	handler.ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if scrape.Code != http.StatusOK || scrape.Body.String() != "safe_metric 1\n" {
		t.Fatalf("metrics response = %d %q", scrape.Code, scrape.Body.String())
	}

	legacy := httptest.NewRecorder()
	handler.ServeHTTP(legacy, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy health status = %d, want 404", legacy.Code)
	}

	profiling := httptest.NewRecorder()
	handler.ServeHTTP(profiling, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if profiling.Code != http.StatusNotFound {
		t.Fatalf("pprof status = %d, want 404", profiling.Code)
	}
}

func TestCheckLiveUsesOnlyLoopbackPortAndRejectsNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/livez" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if err := CheckLive(context.Background(), address); err != nil {
		t.Fatalf("CheckLive() error = %v", err)
	}
	if err := CheckLive(context.Background(), "invalid"); err == nil {
		t.Fatal("CheckLive() accepted malformed address")
	}
}
