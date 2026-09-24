package problem

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithRequestIDAndProblemResponse(t *testing.T) {
	handler := withRequestIDGenerator(NotFound(), func() (string, error) {
		return "req_test", nil
	})
	request := httptest.NewRequest(http.MethodGet, "/missing?secret=hidden", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	if got := response.Header().Get("Content-Type"); got != mediaType {
		t.Fatalf("Content-Type = %q, want %q", got, mediaType)
	}
	if got := response.Header().Get("X-Request-ID"); got != "req_test" {
		t.Fatalf("X-Request-ID = %q, want req_test", got)
	}

	var details Details
	if err := json.NewDecoder(response.Body).Decode(&details); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if details.Code != "not_found" || details.RequestID != "req_test" {
		t.Fatalf("problem details = %+v", details)
	}
	if details.Type != "about:blank" || details.Title != "Resource not found" {
		t.Fatalf("problem details = %+v", details)
	}
}

func TestWithRequestIDFailsClosedWhenRandomnessFails(t *testing.T) {
	handler := withRequestIDGenerator(NotFound(), func() (string, error) {
		return "", errors.New("entropy unavailable")
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get("X-Request-ID"); got != "unavailable" {
		t.Fatalf("X-Request-ID = %q, want unavailable", got)
	}
}

func TestRecoverSanitizesPanic(t *testing.T) {
	handler := WithRequestID(Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("sensitive-token")
	})))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "sensitive-token") {
		t.Fatal("panic value leaked")
	}
}
