package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPMetricsAndTracesAreBoundedAndRedacted(t *testing.T) {
	const canary = "CANARY-payload-secret-api-key-url-query-header-response-audit"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	service := &Service{metrics: NewMetrics(), provider: provider, shutdown: provider.Shutdown}
	handler := service.HTTP("/v1/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		AnnotateAccepted(r.Context(), "evt-safe", []string{"del-safe"})
		w.Header().Set("X-Do-Not-Record", canary)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(canary))
	}))
	request := httptest.NewRequest(http.MethodPost, "https://example.invalid/v1/events?token="+canary,
		strings.NewReader(canary))
	request.Header.Set("Authorization", "Bearer "+canary)
	request.Header.Set("Baggage", "secret="+canary)
	request.Header.Set("Tracestate", "vendor="+canary)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	metrics := scrape(t, service.Metrics().Handler())
	if !strings.Contains(metrics, `wde_http_requests_total{method="POST",route="/v1/events",status_class="2xx"} 1`) {
		t.Fatalf("missing bounded request metric: %s", metrics)
	}
	assertNotContains(t, metrics, canary)
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "POST /v1/events" {
		t.Fatalf("unexpected spans: %#v", spans)
	}
	assertNotContains(t, spans[0].Name()+attributesText(spans[0].Attributes()), canary)
}

func TestArbitraryHTTPMethodIsBoundedInEveryTelemetrySink(t *testing.T) {
	const canary = "CANARY_UNBOUNDED_METHOD"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	service := &Service{metrics: NewMetrics(), provider: provider, shutdown: provider.Shutdown}
	handler := service.HTTP("unmatched", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	request := httptest.NewRequest(canary, "/attacker-controlled", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	metrics := scrape(t, service.Metrics().Handler())
	if !strings.Contains(metrics, `wde_http_requests_total{method="OTHER",route="unmatched",status_class="4xx"} 1`) {
		t.Fatalf("bounded method metric missing: %s", metrics)
	}
	assertNotContains(t, metrics, canary)
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "OTHER unmatched" {
		t.Fatalf("unexpected bounded spans: %#v", spans)
	}
	spanText := spans[0].Name() + attributesText(spans[0].Attributes())
	if !strings.Contains(spanText, "OTHER") {
		t.Fatalf("bounded method attribute missing: %s", spanText)
	}
	assertNotContains(t, spanText, canary)
}

func TestAttemptMetricsUseBoundedCategories(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	service := &Service{metrics: NewMetrics(), provider: provider, shutdown: provider.Shutdown}
	_, finish := service.StartAttempt(context.Background(), AttemptIDs{
		Event: "evt", Delivery: "del", Attempt: "att", Number: 1,
	})
	finish(AttemptResult{Outcome: "retry", Category: "CANARY-unbounded", Duration: time.Millisecond,
		Retry: true, Changed: true})
	metrics := scrape(t, service.Metrics().Handler())
	if !strings.Contains(metrics, `category="other",outcome="retry"`) ||
		!strings.Contains(metrics, `wde_delivery_retries_total{category="other"} 1`) {
		t.Fatalf("bounded attempt metrics missing: %s", metrics)
	}
	assertNotContains(t, metrics, "CANARY-unbounded")
	assertNotContains(t, attributesText(recorder.Ended()[0].Attributes()), "CANARY-unbounded")
	service.Metrics().ObservePurge("CANARY-purge", 1)
	metrics = scrape(t, service.Metrics().Handler())
	if !strings.Contains(metrics, `wde_purge_items_total{category="other"} 1`) {
		t.Fatalf("bounded purge metric missing: %s", metrics)
	}
	assertNotContains(t, metrics, "CANARY-purge")
}

func TestPersistenceSpansAreRelatedBoundedAndRedacted(t *testing.T) {
	const canary = "CANARY-persistence-error-payload-tenant-url"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	service := &Service{metrics: NewMetrics(), provider: provider, shutdown: provider.Shutdown}
	claimCtx, finishClaim := service.StartClaim(context.Background(), 4)
	finishClaim(0, errors.New(canary))
	attemptCtx, finishAttempt := service.StartAttempt(claimCtx, AttemptIDs{
		Event: "018f0000-0000-7000-8000-000000000001", Delivery: "018f0000-0000-7000-8000-000000000002",
		Attempt: "018f0000-0000-7000-8000-000000000003", Number: 1,
	})
	_, finishFinalization := service.StartFinalization(attemptCtx, PersistenceIDs{
		Delivery: "018f0000-0000-7000-8000-000000000002", Attempt: "018f0000-0000-7000-8000-000000000003",
	})
	finishFinalization(false, errors.New(canary))
	finishAttempt(AttemptResult{Outcome: "permanent_failure", Category: "other", Changed: false})

	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("ended spans=%d, want 3", len(spans))
	}
	names := make(map[string]bool, len(spans))
	var attemptID, finalParent string
	for _, span := range spans {
		names[span.Name()] = true
		assertNotContains(t, span.Name()+attributesText(span.Attributes())+span.Status().Description, canary)
		if span.Name() == "webhook.delivery.attempt" {
			attemptID = span.SpanContext().SpanID().String()
		}
		if span.Name() == "wde.persistence.finalize" {
			finalParent = span.Parent().SpanID().String()
		}
	}
	for _, name := range []string{"wde.persistence.claim", "webhook.delivery.attempt", "wde.persistence.finalize"} {
		if !names[name] {
			t.Fatalf("missing span %q: %#v", name, names)
		}
	}
	if attemptID == "" || finalParent != attemptID {
		t.Fatalf("finalization parent=%q attempt=%q", finalParent, attemptID)
	}
}

func TestSafeRemoteParentDropsTraceStateAndBaggage(t *testing.T) {
	header := make(http.Header)
	header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	header.Set("Tracestate", "vendor=CANARY-state")
	header.Set("Baggage", "secret=CANARY-baggage")
	ctx := safeRemoteParent(context.Background(), header)
	span := sdktrace.NewTracerProvider().Tracer("test")
	_, child := span.Start(ctx, "child")
	parent := child.SpanContext()
	child.End()
	if parent.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || parent.TraceState().Len() != 0 {
		t.Fatalf("unsafe or missing parent context: %#v", parent)
	}
}

func TestRemoteParentCannotOverrideLocalSamplingPolicy(t *testing.T) {
	for _, ratio := range []float64{0, 1} {
		for _, sampled := range []bool{false, true} {
			for _, authenticated := range []bool{false, true} {
				name := fmt.Sprintf("ratio_%g/sampled_%t/authenticated_%t", ratio, sampled, authenticated)
				t.Run(name, func(t *testing.T) {
					recorder := tracetest.NewSpanRecorder()
					provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder),
						sdktrace.WithSampler(secureSampler(ratio)))
					service := &Service{metrics: NewMetrics(), provider: provider, shutdown: provider.Shutdown}
					var pepper [32]byte
					pepper[0] = 1
					token, prefix, verifier, err := auth.Generate(true, pepper)
					if err != nil {
						t.Fatal(err)
					}
					authenticator := auth.NewAuthenticator(telemetryLookup{prefix: prefix, record: auth.Record{
						APIKeyID: uuid.New(), WorkspaceID: uuid.New(), Verifier: verifier[:],
						Status: "active", WorkspaceStatus: "active",
					}}, pepper)
					next := authenticator.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusAccepted)
					}))
					handler := service.HTTP("/v1/events", next)
					request := httptest.NewRequest(http.MethodPost, "/v1/events", nil)
					flag := "00"
					if sampled {
						flag = "01"
					}
					request.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-"+flag)
					if authenticated {
						request.Header.Set("Authorization", "Bearer "+token)
					}
					handler.ServeHTTP(httptest.NewRecorder(), request)
					if got, want := len(recorder.Ended()), int(ratio); got != want {
						t.Fatalf("ended spans = %d, want %d", got, want)
					}
					_ = provider.Shutdown(context.Background())
				})
			}
		}
	}
}

type telemetryLookup struct {
	prefix string
	record auth.Record
}

func (l telemetryLookup) LookupKey(_ context.Context, prefix string) (auth.Record, bool, error) {
	return l.record, prefix == l.prefix, nil
}

func TestPanicCanaryIsSanitizedAndSpanStillEnds(t *testing.T) {
	const canary = "CANARY-panic-error-secret"
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	service := &Service{metrics: NewMetrics(), provider: provider, shutdown: provider.Shutdown}
	handler := problem.Recover(service.HTTP("/v1/events", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(canary)
	})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/events", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d", response.Code)
	}
	assertNotContains(t, response.Body.String(), canary)
	metrics := scrape(t, service.Metrics().Handler())
	if !strings.Contains(metrics, `status_class="5xx"`) {
		t.Fatalf("panic metric missing: %s", metrics)
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("panic left unfinished spans: %d", len(spans))
	}
	assertNotContains(t, spans[0].Name()+attributesText(spans[0].Attributes()), canary)
}

func TestNoopAndExporterFailureDoNotBreakMetrics(t *testing.T) {
	service := NewNoop()
	service.Metrics().SetQueue(3, time.Second)
	if body := scrape(t, service.Metrics().Handler()); !strings.Contains(body, "wde_queue_ready 3") {
		t.Fatalf("no-op service did not expose metrics: %s", body)
	}
	rejecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer rejecting.Close()
	exported, err := New(context.Background(), Options{Service: "test", Environment: "test",
		OTLPEndpoint: rejecting.URL, ExportTimeout: 100 * time.Millisecond, TraceSampleRatio: 1})
	if err != nil {
		t.Fatalf("exporter construction must not need a live collector: %v", err)
	}
	_, span := exported.Tracer().Start(context.Background(), "safe")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = exported.Shutdown(ctx)
}

func scrape(t *testing.T, handler http.Handler) string {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	data, err := io.ReadAll(response.Result().Body)
	if err != nil || response.Code != http.StatusOK {
		t.Fatalf("scrape failed: status=%d err=%v", response.Code, err)
	}
	return string(data)
}

func attributesText(attributes any) string { return fmt.Sprint(attributes) }

func assertNotContains(t *testing.T, value, forbidden string) {
	t.Helper()
	if strings.Contains(value, forbidden) {
		t.Fatalf("sensitive canary leaked: %q", forbidden)
	}
}
