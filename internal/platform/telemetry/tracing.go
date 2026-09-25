package telemetry

import (
	"context"
	"errors"
	"math"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type Options struct {
	Service, Version, Environment string
	OTLPEndpoint                  string
	ExportTimeout                 time.Duration
	TraceSampleRatio              float64
}

// Service owns process telemetry without modifying OpenTelemetry globals.
type Service struct {
	metrics  *Metrics
	provider trace.TracerProvider
	shutdown func(context.Context) error
}

func New(ctx context.Context, options Options) (*Service, error) {
	service := &Service{metrics: NewMetrics(), provider: noop.NewTracerProvider()}
	service.shutdown = func(context.Context) error { return nil }
	if options.ExportTimeout <= 0 || options.ExportTimeout > 10*time.Second ||
		math.IsNaN(options.TraceSampleRatio) || math.IsInf(options.TraceSampleRatio, 0) ||
		options.TraceSampleRatio < 0 || options.TraceSampleRatio > 1 {
		return service, errors.New("telemetry: invalid exporter settings")
	}
	if options.OTLPEndpoint == "" {
		return service, nil
	}
	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(traceEndpoint(options.OTLPEndpoint)),
		otlptracehttp.WithTimeout(options.ExportTimeout))
	if err != nil {
		return service, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithExportTimeout(options.ExportTimeout)),
		sdktrace.WithSampler(secureSampler(options.TraceSampleRatio)),
		sdktrace.WithResource(resource.NewSchemaless(
			attribute.String("service.name", options.Service),
			attribute.String("service.version", options.Version),
			attribute.String("deployment.environment.name", options.Environment),
		)),
	)
	service.provider, service.shutdown = provider, provider.Shutdown
	return service, nil
}

func traceEndpoint(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil && (parsed.Path == "" || parsed.Path == "/") {
		parsed.Path = "/v1/traces"
		return parsed.String()
	}
	return raw
}

func secureSampler(ratio float64) sdktrace.Sampler {
	local := sdktrace.TraceIDRatioBased(ratio)
	return sdktrace.ParentBased(local,
		sdktrace.WithRemoteParentSampled(local),
		sdktrace.WithRemoteParentNotSampled(local))
}

func NewNoop() *Service {
	service, _ := New(context.Background(), Options{ExportTimeout: time.Second, TraceSampleRatio: 0})
	return service
}

func (s *Service) Metrics() *Metrics { return s.metrics }
func (s *Service) Tracer() trace.Tracer {
	return s.provider.Tracer("github.com/gabrieldosprazeres/go-webhook-delivery")
}
func (s *Service) Shutdown(ctx context.Context) error { return s.shutdown(ctx) }
