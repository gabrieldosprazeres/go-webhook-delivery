package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestNewJSONDropsFieldsOutsideAllowlist(t *testing.T) {
	const canary = "wde-secret-canary-8425"
	var output bytes.Buffer
	logger := NewJSON(&output, Options{Service: "api", Version: "test", Environment: "test"})

	logger.Info("request handled",
		slog.String("request_id", "req-safe"),
		slog.String("payload", canary),
		slog.String("api_key", canary),
		slog.String("dsn", canary),
	)

	got := output.String()
	if strings.Contains(got, canary) || strings.Contains(got, "payload") || strings.Contains(got, "api_key") || strings.Contains(got, "dsn") {
		t.Fatalf("log contains a forbidden field: %s", got)
	}
	if !strings.Contains(got, `"request_id":"req-safe"`) {
		t.Fatalf("log does not contain allowed request ID: %s", got)
	}
}

func TestNewJSONHonorsLevel(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSON(&output, Options{Service: "worker", Version: "test", Environment: "test", Level: "warn"})

	logger.Info("not written")
	logger.Warn("written")

	if strings.Contains(output.String(), "not written") || !strings.Contains(output.String(), "written") {
		t.Fatalf("unexpected level filtering: %s", output.String())
	}
}

func TestNewJSONTruncatesMessagesStringsAndErrors(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSON(&output, Options{Service: "api", Version: "test", Environment: "test"})
	longValue := strings.Repeat("ç", MaxAttributeValueBytes)

	logger.Info(longValue,
		slog.String("request_id", longValue),
		slog.Any("code", errors.New(longValue)),
	)

	var entry map[string]any
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	for _, key := range []string{"msg", "request_id", "code"} {
		value, ok := entry[key].(string)
		if !ok {
			t.Fatalf("%s is not a string: %#v", key, entry[key])
		}
		if len(value) > MaxAttributeValueBytes || !strings.HasSuffix(value, "...") {
			t.Fatalf("%s was not bounded safely: len=%d value=%q", key, len(value), value)
		}
	}
}
