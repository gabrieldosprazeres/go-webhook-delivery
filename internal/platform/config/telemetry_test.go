package config

import (
	"strings"
	"testing"
	"time"
)

func TestTelemetryConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		wantErr string
	}{
		{name: "valid local", values: map[string]string{"WDE_OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318", "WDE_OTEL_EXPORT_TIMEOUT": "750ms", "WDE_OTEL_TRACE_SAMPLE_RATIO": "0.25"}},
		{name: "invalid endpoint credentials", values: map[string]string{"WDE_OTEL_EXPORTER_OTLP_ENDPOINT": "https://token@example.com"}, wantErr: "invalid"},
		{name: "invalid endpoint query", values: map[string]string{"WDE_OTEL_EXPORTER_OTLP_ENDPOINT": "https://example.com?token=x"}, wantErr: "invalid"},
		{name: "invalid timeout", values: map[string]string{"WDE_OTEL_EXPORT_TIMEOUT": "50ms"}, wantErr: "between"},
		{name: "zero timeout", values: map[string]string{"WDE_OTEL_EXPORT_TIMEOUT": "0s"}, wantErr: "between"},
		{name: "invalid ratio", values: map[string]string{"WDE_OTEL_TRACE_SAMPLE_RATIO": "1.1"}, wantErr: "between"},
		{name: "nan ratio", values: map[string]string{"WDE_OTEL_TRACE_SAMPLE_RATIO": "NaN"}, wantErr: "between"},
		{name: "positive infinity ratio", values: map[string]string{"WDE_OTEL_TRACE_SAMPLE_RATIO": "+Inf"}, wantErr: "between"},
		{name: "negative infinity ratio", values: map[string]string{"WDE_OTEL_TRACE_SAMPLE_RATIO": "-Inf"}, wantErr: "between"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"WDE_DATABASE_URL": "postgres://test:test@localhost:5432/wde?sslmode=disable"}
			for key, value := range test.values {
				values[key] = value
			}
			cfg, err := Load(LoadOptions{Service: ServiceWorker, LookupEnv: mapLookup(values)})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Load() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Telemetry.ExportTimeout != 750*time.Millisecond || cfg.Telemetry.TraceSampleRatio != 0.25 {
				t.Fatalf("unexpected telemetry config: %#v", cfg.Telemetry)
			}
		})
	}
}

func TestProductionRequiresHTTPSOTLP(t *testing.T) {
	cfg := Config{Profile: ProfileProduction, Telemetry: TelemetryConfig{
		OTLPEndpoint: "http://collector.internal:4318", ExportTimeout: time.Second, TraceSampleRatio: 0.1,
	}}
	if err := cfg.validateTelemetry(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("validateTelemetry() error = %v, want HTTPS rejection", err)
	}
}

func TestAPIGlobalShutdownBudgetIsCappedAtThirtySeconds(t *testing.T) {
	_, err := Load(LoadOptions{Service: ServiceAPI, LookupEnv: mapLookup(map[string]string{
		"WDE_DATABASE_URL":     "postgres://test:test@localhost:5432/wde?sslmode=disable",
		"WDE_SHUTDOWN_TIMEOUT": "31s",
	})})
	if err == nil || !strings.Contains(err.Error(), "WDE_SHUTDOWN_TIMEOUT") {
		t.Fatalf("Load() error = %v, want shutdown cap rejection", err)
	}
}
