package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	appruntime "github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/runtime"
)

type blockingTelemetry struct {
	called bool
	err    error
}

func (f *blockingTelemetry) Shutdown(ctx context.Context) error {
	f.called = true
	if f.err != nil {
		return f.err
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestTelemetryFlushConsumesOnlyRemainingGlobalShutdownBudget(t *testing.T) {
	budget := appruntime.NewShutdownBudget(70 * time.Millisecond)
	started := time.Now()
	coreCtx, cancelCore := budget.Context(0)
	defer cancelCore()
	<-coreCtx.Done() // Simulate a core phase consuming the entire budget.
	flush := &blockingTelemetry{}
	stopTelemetry(flush, budget, time.Second, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if !flush.called {
		t.Fatal("telemetry flush was skipped")
	}
	if elapsed := time.Since(started); elapsed > 160*time.Millisecond {
		t.Fatalf("shutdown phases exceeded global budget: %s", elapsed)
	}
}

func TestTelemetryFlushFailureIsLoggedAndDoesNotReplaceShutdownResult(t *testing.T) {
	var output bytes.Buffer
	flush := &blockingTelemetry{err: errors.New("CANARY exporter secret")}
	stopTelemetry(flush, appruntime.NewShutdownBudget(time.Second), time.Second,
		slog.New(slog.NewTextHandler(&output, nil)))
	if !flush.called || !strings.Contains(output.String(), "exporter_shutdown") {
		t.Fatalf("flush failure was not safely reported: %s", output.String())
	}
	if strings.Contains(output.String(), "CANARY") {
		t.Fatalf("flush failure leaked error: %s", output.String())
	}
}
