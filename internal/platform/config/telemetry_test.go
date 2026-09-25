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

func TestProductionAllowsOnlyExplicitPrivateHTTPCollector(t *testing.T) {
	valid := Config{Profile: ProfileProduction, Telemetry: TelemetryConfig{
		OTLPEndpoint: "http://otel-collector:4318", ExportTimeout: time.Second,
		TraceSampleRatio: 0.1, AllowPrivateHTTP: true,
	}}
	if err := valid.validateTelemetry(); err != nil {
		t.Fatalf("private collector rejected: %v", err)
	}
	for _, endpoint := range []string{"http://otel-collector:4318/v1/traces", "http://other:4318"} {
		invalid := valid
		invalid.Telemetry.OTLPEndpoint = endpoint
		if err := invalid.validateTelemetry(); err == nil {
			t.Fatalf("unsafe private endpoint accepted: %s", endpoint)
		}
	}
	withoutOptIn := valid
	withoutOptIn.Telemetry.AllowPrivateHTTP = false
	if err := withoutOptIn.validateTelemetry(); err == nil {
		t.Fatal("private HTTP collector accepted without opt-in")
	}
}

func TestConsoleConfigurationUsesFixedSessionPolicyAndExactOrigin(t *testing.T) {
	valid := Config{Service: ServiceConsole, Profile: ProfileProduction,
		Console: ConsoleConfig{Origin: "https://console.example.test", SessionIdle: 15 * time.Minute,
			SessionAbsolute: time.Hour}}
	if err := valid.validateConsole(); err != nil {
		t.Fatalf("valid console config rejected: %v", err)
	}
	for _, origin := range []string{"http://console.example.test", "https://console.example.test/path", "https://user@console.example.test"} {
		invalid := valid
		invalid.Console.Origin = origin
		if err := invalid.validateConsole(); err == nil {
			t.Fatalf("invalid origin accepted: %s", origin)
		}
	}
	invalidPolicy := valid
	invalidPolicy.Console.SessionIdle = 30 * time.Minute
	if err := invalidPolicy.validateConsole(); err == nil {
		t.Fatal("mutable console session policy accepted")
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
