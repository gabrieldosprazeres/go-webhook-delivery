package telemetry

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// HTTP instruments a known route without recording URL, query, headers or bodies.
func (s *Service) HTTP(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started := time.Now()
		parent := safeRemoteParent(request.Context(), request.Header)
		method := boundedMethod(request.Method)
		ctx, span := s.Tracer().Start(parent, method+" "+route,
			trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
				attribute.String("http.request.method", method),
				attribute.String("http.route", route),
			))
		if requestID := problem.RequestID(ctx); requestID != "" {
			span.SetAttributes(attribute.String("wde.request.id", requestID))
		}
		writer := &statusWriter{ResponseWriter: w}
		defer func() {
			recovered := recover()
			status := writer.status
			if recovered != nil {
				status = http.StatusInternalServerError
			} else if status == 0 {
				status = http.StatusOK
			}
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= 500 {
				span.SetStatus(codes.Error, "server error")
			}
			span.End()
			s.metrics.observeHTTP(route, method, status, time.Since(started))
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func safeRemoteParent(ctx context.Context, header http.Header) context.Context {
	extracted := propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(header))
	spanContext := trace.SpanContextFromContext(extracted)
	if !spanContext.IsValid() {
		return ctx
	}
	safe := trace.NewSpanContext(trace.SpanContextConfig{TraceID: spanContext.TraceID(),
		SpanID: spanContext.SpanID(), TraceFlags: spanContext.TraceFlags(), Remote: true})
	return trace.ContextWithRemoteSpanContext(ctx, safe)
}

// Inject adds only W3C trace context to an outbound request.
func Inject(ctx context.Context, request *http.Request) {
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(request.Header))
}

func AnnotateAccepted(ctx context.Context, eventID string, deliveries []string) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("wde.event.id", eventID),
		attribute.Int("wde.delivery.count", len(deliveries)))
	for index, deliveryID := range deliveries {
		span.AddEvent("delivery.created", trace.WithAttributes(
			attribute.String("wde.delivery.id", deliveryID), attribute.String("wde.delivery.index", strconv.Itoa(index))))
	}
}
