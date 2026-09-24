package config

import (
	"errors"
	"time"
)

// EdgeConfig bounds unauthenticated work per API instance before credential lookup.
type EdgeConfig struct {
	MaxInFlight                        int
	Global, Origin, Prefix, MaxBuckets int
	Window                             time.Duration
}

func defaultEdgeConfig() EdgeConfig {
	return EdgeConfig{
		MaxInFlight: 256, Global: 60000, Origin: 600,
		Prefix: 300, MaxBuckets: 4096, Window: time.Minute,
	}
}

func applyEdgeEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	values := []struct {
		name   string
		target *int
	}{
		{"WDE_EDGE_MAX_IN_FLIGHT", &cfg.Edge.MaxInFlight},
		{"WDE_EDGE_GLOBAL", &cfg.Edge.Global},
		{"WDE_EDGE_ORIGIN", &cfg.Edge.Origin},
		{"WDE_EDGE_PREFIX", &cfg.Edge.Prefix},
		{"WDE_EDGE_MAX_BUCKETS", &cfg.Edge.MaxBuckets},
	}
	for _, item := range values {
		value, err := intEnv(lookup, item.name, *item.target)
		if err != nil {
			return err
		}
		*item.target = value
	}
	window, err := durationEnv(lookup, "WDE_EDGE_WINDOW", cfg.Edge.Window)
	if err != nil {
		return err
	}
	cfg.Edge.Window = window
	return nil
}

func (cfg Config) validateEdge() error {
	edge := cfg.Edge
	if edge.MaxInFlight < 1 || edge.MaxInFlight > 10000 || edge.Global < 1 || edge.Global > 1000000 ||
		edge.Origin < 1 || edge.Origin > edge.Global || edge.Prefix < 1 || edge.Prefix > edge.Global ||
		edge.MaxBuckets < 16 || edge.MaxBuckets > 65536 || edge.Window < time.Second ||
		edge.Window > time.Hour || edge.Window%time.Second != 0 {
		return errors.New("config: edge limiter bounds are invalid")
	}
	return nil
}
