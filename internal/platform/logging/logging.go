// Package logging creates structured loggers that accept only approved fields.
package logging

import (
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"
)

// MaxAttributeValueBytes bounds every textual value and log message.
const MaxAttributeValueBytes = 256

// Options describes the safe, non-secret process identity added to each log entry.
type Options struct {
	Service     string
	Version     string
	Environment string
	Level       string
}

// NewJSON returns a JSON slog logger that drops attributes outside the project allowlist.
func NewJSON(output io.Writer, opts Options) *slog.Logger {
	level := new(slog.LevelVar)
	level.Set(parseLevel(opts.Level))

	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 {
				switch attr.Key {
				case slog.TimeKey, slog.LevelKey, slog.SourceKey:
					return attr
				case slog.MessageKey:
					return slog.String(attr.Key, truncate(attr.Value.String()))
				}
			}
			if !isAllowedAttribute(attr.Key) {
				return slog.Attr{}
			}
			return sanitizeAttribute(attr)
		},
	})

	return slog.New(handler).With(
		slog.String("service", opts.Service),
		slog.String("version", opts.Version),
		slog.String("environment", opts.Environment),
	)
}

func isAllowedAttribute(key string) bool {
	switch key {
	case "service", "version", "environment", "request_id", "trace_id", "event_id",
		"delivery_id", "attempt_id", "status", "code", "duration_ms", "count", "component",
		"backlog", "oldest_age", "batches", "degraded", "deleted", "payloads", "secrets",
		"attempts", "deliveries", "events", "audits", "buckets", "workspace_rows":
		return true
	default:
		return false
	}
}

func sanitizeAttribute(attr slog.Attr) slog.Attr {
	value := attr.Value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, truncate(value.String()))
	case slog.KindBool, slog.KindDuration, slog.KindFloat64, slog.KindInt64, slog.KindTime, slog.KindUint64:
		return slog.Attr{Key: attr.Key, Value: value}
	case slog.KindAny:
		return slog.String(attr.Key, "<redacted>")
	default:
		return slog.String(attr.Key, "<unsupported>")
	}
}

func truncate(value string) string {
	if len(value) <= MaxAttributeValueBytes {
		return value
	}
	limit := MaxAttributeValueBytes - len("...")
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + "..."
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
