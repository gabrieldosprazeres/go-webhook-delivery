package delivery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/config"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/cryptobox"
	"github.com/google/uuid"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestRetryPolicyUsesDeterministicFullJitterAndCap(t *testing.T) {
	var ceilings []time.Duration
	policy := RetryPolicy{
		Base: time.Second,
		Cap:  8 * time.Second,
		Jitter: func(ceiling time.Duration) time.Duration {
			ceilings = append(ceilings, ceiling)
			return ceiling / 2
		},
	}
	for attempt, want := range []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		if got := policy.Delay(int16(attempt+1), 0); got != want {
			t.Fatalf("attempt %d delay=%s want=%s", attempt+1, got, want)
		}
	}
	if got := policy.Delay(1, 20*time.Second); got != 500*time.Millisecond {
		t.Fatalf("above-cap server delay=%s want full-jitter fallback", got)
	}
	wantCeilings := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second, time.Second}
	for index := range wantCeilings {
		if ceilings[index] != wantCeilings[index] {
			t.Fatalf("ceiling[%d]=%s want=%s", index, ceilings[index], wantCeilings[index])
		}
	}
}

func TestPollPolicyUsesBoundedDeterministicBackoff(t *testing.T) {
	var spans []time.Duration
	policy := defaultPollPolicy(100*time.Millisecond, func(span time.Duration) time.Duration {
		spans = append(spans, span)
		return span
	})
	for index, want := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond, 1600 * time.Millisecond} {
		if got := policy.Delay(int16(index + 1)); got != want {
			t.Fatalf("empty poll %d delay=%s want=%s", index+1, got, want)
		}
	}
	if len(spans) != 6 || spans[0] != 50*time.Millisecond || spans[4] != 800*time.Millisecond {
		t.Fatalf("jitter spans=%v", spans)
	}
}

func TestExecuteClassifiesNetworkAndTimeout(t *testing.T) {
	tests := []struct {
		name, category string
		err            error
	}{
		{"network", "network", errors.New("connection refused")},
		{"timeout", "timeout", context.DeadlineExceeded},
		{"cancelled", "cancelled", context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &Runner{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, test.err
			})}}
			request, err := http.NewRequest(http.MethodPost, "https://example.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			result := runner.execute(request)
			if result.Disposition != DispositionRetry || result.Category != test.category {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestExecuteTreatsRedirectAsPermanent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	runner := NewRunner(nil, cryptobox.Materials{}, config.ProfileTest, true, uuid.New())
	request, err := http.NewRequest(http.MethodPost, server.URL+"/redirect", nil)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.execute(request)
	if result.Disposition != DispositionPermanent || result.Category != "redirect" ||
		result.HTTPStatus == nil || *result.HTTPStatus != http.StatusFound {
		t.Fatalf("result=%+v", result)
	}
}

func TestExecuteClassifiesHTTPAndRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		status      int
		retryAfter  string
		disposition Disposition
		wantDelay   time.Duration
	}{
		{"success", 204, "", DispositionSuccess, 0},
		{"redirect", 302, "", DispositionPermanent, 0},
		{"client", 422, "", DispositionPermanent, 0},
		{"timeout", 408, "", DispositionRetry, 0},
		{"too early", 425, "", DispositionRetry, 0},
		{"rate limit seconds", 429, "120", DispositionRetry, 2 * time.Minute},
		{"rate limit above cap ignored", 429, "3600", DispositionRetry, 0},
		{"rate limit overflow ignored", 429, "2147483647", DispositionRetry, 0},
		{"rate limit invalid ignored", 429, "later", DispositionRetry, 0},
		{"server", 503, now.Add(3 * time.Minute).Format(http.TimeFormat), DispositionRetry, 3 * time.Minute},
		{"server date above cap ignored", 503, now.Add(20 * time.Minute).Format(http.TimeFormat), DispositionRetry, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
			}))
			defer server.Close()
			runner := &Runner{client: server.Client(), retry: DefaultRetryPolicy(), now: func() time.Time { return now }}
			request, err := http.NewRequest(http.MethodPost, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			result := runner.execute(request)
			if result.Disposition != test.disposition || result.RetryAfter != test.wantDelay {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestExecuteRejectsHostileHTTPStatus(t *testing.T) {
	for _, status := range []int{600, 699, 999} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			runner := &Runner{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(http.NoBody), Header: make(http.Header)}, nil
			})}, retry: DefaultRetryPolicy(), now: time.Now}
			request, err := http.NewRequest(http.MethodPost, "https://example.test", nil)
			if err != nil {
				t.Fatal(err)
			}
			result := runner.execute(request)
			if result.Disposition != DispositionPermanent || result.Category != "invalid_http_status" || result.HTTPStatus != nil {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
