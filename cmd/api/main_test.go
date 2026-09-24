package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicListenerDoesNotExposeProbes(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		publicRoutes().ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d", path, response.Code, http.StatusNotFound)
		}
		if response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s X-Request-ID is empty", path)
		}
	}
}
