package config

import (
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TelemetryConfig contains only non-secret exporter settings.
type TelemetryConfig struct {
	OTLPEndpoint     string
	ExportTimeout    time.Duration
	TraceSampleRatio float64
	AllowPrivateHTTP bool
}

func defaultTelemetryConfig() TelemetryConfig {
	return TelemetryConfig{ExportTimeout: 5 * time.Second, TraceSampleRatio: 0.1}
}

func applyTelemetryEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	assignEnv(lookup, "WDE_OTEL_EXPORTER_OTLP_ENDPOINT", &cfg.Telemetry.OTLPEndpoint)
	var err error
	cfg.Telemetry.ExportTimeout, err = durationEnv(lookup, "WDE_OTEL_EXPORT_TIMEOUT", cfg.Telemetry.ExportTimeout)
	if err != nil {
		return err
	}
	if raw, ok := lookup("WDE_OTEL_TRACE_SAMPLE_RATIO"); ok {
		cfg.Telemetry.TraceSampleRatio, err = strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return errors.New("config: WDE_OTEL_TRACE_SAMPLE_RATIO must be a number")
		}
	}
	cfg.Telemetry.AllowPrivateHTTP, err = boolEnv(lookup, "WDE_OTEL_ALLOW_PRIVATE_HTTP", false)
	if err != nil {
		return err
	}
	return nil
}

func (cfg Config) validateTelemetry() error {
	if cfg.Telemetry.ExportTimeout < 100*time.Millisecond || cfg.Telemetry.ExportTimeout > 10*time.Second {
		return errors.New("config: WDE_OTEL_EXPORT_TIMEOUT must be between 100ms and 10s")
	}
	if math.IsNaN(cfg.Telemetry.TraceSampleRatio) || math.IsInf(cfg.Telemetry.TraceSampleRatio, 0) ||
		cfg.Telemetry.TraceSampleRatio < 0 || cfg.Telemetry.TraceSampleRatio > 1 {
		return errors.New("config: WDE_OTEL_TRACE_SAMPLE_RATIO must be between 0 and 1")
	}
	endpoint := strings.TrimSpace(cfg.Telemetry.OTLPEndpoint)
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("config: WDE_OTEL_EXPORTER_OTLP_ENDPOINT is invalid")
	}
	if cfg.Profile == ProfileProduction && parsed.Scheme != "https" {
		private := cfg.Telemetry.AllowPrivateHTTP && endpoint == "http://otel-collector:4318" && parsed.Path == ""
		if !private {
			return errors.New("config: production OTLP must use https unless it is the opted-in private collector")
		}
	}
	return nil
}
