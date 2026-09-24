package config

import (
	"errors"
	"time"
)

type QuotaPolicy struct {
	Global, Workspace, APIKey, Resource int
	Window                              time.Duration
}

type QuotaConfig struct {
	Ingest, EndpointWrite, Query, Replay QuotaPolicy
}

func defaultQuotas() QuotaConfig {
	return QuotaConfig{
		Ingest:        QuotaPolicy{Global: 10000, Workspace: 200, APIKey: 200, Window: time.Second},
		EndpointWrite: QuotaPolicy{Global: 6000, Workspace: 60, APIKey: 60, Window: time.Minute},
		Query:         QuotaPolicy{Global: 30000, Workspace: 300, APIKey: 300, Window: time.Minute},
		Replay:        QuotaPolicy{Global: 1000, Workspace: 10, APIKey: 10, Resource: 2, Window: time.Minute},
	}
}

func applyQuotaEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	policies := []struct {
		prefix string
		policy *QuotaPolicy
	}{
		{"WDE_QUOTA_INGEST", &cfg.Quotas.Ingest},
		{"WDE_QUOTA_ENDPOINT_WRITE", &cfg.Quotas.EndpointWrite},
		{"WDE_QUOTA_QUERY", &cfg.Quotas.Query},
		{"WDE_QUOTA_REPLAY", &cfg.Quotas.Replay},
	}
	for _, item := range policies {
		values := []struct {
			name   string
			target *int
		}{{"_GLOBAL", &item.policy.Global}, {"_WORKSPACE", &item.policy.Workspace},
			{"_API_KEY", &item.policy.APIKey}, {"_RESOURCE", &item.policy.Resource}}
		for _, value := range values {
			parsed, err := intEnv(lookup, item.prefix+value.name, *value.target)
			if err != nil {
				return err
			}
			*value.target = parsed
		}
		window, err := durationEnv(lookup, item.prefix+"_WINDOW", item.policy.Window)
		if err != nil {
			return err
		}
		item.policy.Window = window
	}
	return nil
}

func (cfg Config) validateQuotas() error {
	policies := []QuotaPolicy{cfg.Quotas.Ingest, cfg.Quotas.EndpointWrite, cfg.Quotas.Query, cfg.Quotas.Replay}
	for _, policy := range policies {
		if policy.Global < 1 || policy.Global > 1000000 || policy.Workspace < 1 ||
			policy.Workspace > policy.Global || policy.APIKey < 1 || policy.APIKey > policy.Workspace ||
			policy.Resource < 0 || policy.Resource > policy.APIKey || policy.Window < time.Second ||
			policy.Window > time.Hour || policy.Window%time.Second != 0 {
			return errors.New("config: quota limits or windows are invalid")
		}
	}
	return nil
}
