package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoginMiddlewareUsesFormCredentialPrefix(t *testing.T) {
	limiter := NewEdge(EdgePolicy{MaxInFlight: 2, Global: 10, Origin: 10, Prefix: 1,
		MaxBuckets: 10, Window: time.Minute}, [32]byte{1})
	handler := limiter.LoginMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("api_key") == "" {
			t.Fatal("parsed login form was not preserved for the handler")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	token := "wde_test_0000000000000000_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	request := func(value string) *httptest.ResponseRecorder {
		form := url.Values{"api_key": {value}}
		r := httptest.NewRequest(http.MethodPost, "http://console.test/session", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "192.0.2.10:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(token); response.Code != http.StatusNoContent {
		t.Fatalf("first status=%d", response.Code)
	}
	if response := request(token); response.Code != http.StatusTooManyRequests {
		t.Fatalf("same prefix status=%d", response.Code)
	}
	other := "wde_test_1111111111111111_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if response := request(other); response.Code != http.StatusNoContent {
		t.Fatalf("independent prefix status=%d", response.Code)
	}
}

func TestEdgeLimiterBoundsAnonymousOriginAndCardinality(t *testing.T) {
	limiter := NewEdge(EdgePolicy{
		MaxInFlight: 1, Global: 100, Origin: 1, Prefix: 1,
		MaxBuckets: 16, Window: time.Minute,
	}, [32]byte{1})
	called := 0
	handler := limiter.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called++ }))
	for index := range 2 {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "192.0.2.10:1234"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if index == 0 && response.Code != http.StatusOK || index == 1 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("request=%d status=%d", index, response.Code)
		}
	}
	if called != 1 {
		t.Fatalf("downstream calls=%d", called)
	}
	for index := range 40 {
		limiter.allow("192.0.2."+string(rune('A'+index)), "")
	}
	if len(limiter.items) > limiter.policy.MaxBuckets {
		t.Fatalf("local bucket cardinality=%d max=%d", len(limiter.items), limiter.policy.MaxBuckets)
	}
}

func TestEdgeLimiterSemaphoreRejectsConcurrentOverflow(t *testing.T) {
	limiter := NewEdge(EdgePolicy{
		MaxInFlight: 1, Global: 100, Origin: 100, Prefix: 100,
		MaxBuckets: 16, Window: time.Minute,
	}, [32]byte{1})
	entered, release := make(chan struct{}), make(chan struct{})
	handler := limiter.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}))
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	<-entered
	overflow := httptest.NewRecorder()
	handler.ServeHTTP(overflow, httptest.NewRequest(http.MethodGet, "/", nil))
	if overflow.Code != http.StatusTooManyRequests || overflow.Header().Get("Retry-After") != "1" {
		t.Fatalf("overflow status=%d retry=%q", overflow.Code, overflow.Header().Get("Retry-After"))
	}
	close(release)
	group.Wait()
}
