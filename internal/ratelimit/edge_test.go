package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

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
