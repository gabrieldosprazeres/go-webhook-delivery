package config

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

type ConsoleConfig struct {
	Origin          string
	SessionIdle     time.Duration
	SessionAbsolute time.Duration
}

func defaultConsoleConfig() ConsoleConfig {
	return ConsoleConfig{
		Origin:          "http://127.0.0.1:8082",
		SessionIdle:     15 * time.Minute,
		SessionAbsolute: 60 * time.Minute,
	}
}

func applyConsoleEnvironment(cfg *Config, lookup func(string) (string, bool)) error {
	if cfg.Service != ServiceConsole {
		return nil
	}
	assignEnv(lookup, "WDE_CONSOLE_ORIGIN", &cfg.Console.Origin)
	var err error
	cfg.Console.SessionIdle, err = durationEnv(lookup, "WDE_CONSOLE_SESSION_IDLE", cfg.Console.SessionIdle)
	if err != nil {
		return err
	}
	cfg.Console.SessionAbsolute, err = durationEnv(lookup, "WDE_CONSOLE_SESSION_ABSOLUTE", cfg.Console.SessionAbsolute)
	return err
}

func (cfg Config) validateConsole() error {
	if cfg.Service != ServiceConsole {
		return nil
	}
	if cfg.Console.SessionIdle != 15*time.Minute || cfg.Console.SessionAbsolute != 60*time.Minute {
		return errors.New("config: console session policy must be 15m idle and 60m absolute")
	}
	origin, err := url.Parse(strings.TrimSpace(cfg.Console.Origin))
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" ||
		origin.Path != "" || (origin.Scheme != "http" && origin.Scheme != "https") {
		return errors.New("config: WDE_CONSOLE_ORIGIN must be an origin without path")
	}
	if cfg.Profile == ProfileProduction && origin.Scheme != "https" {
		return errors.New("config: WDE_CONSOLE_ORIGIN must use https in production")
	}
	return nil
}
