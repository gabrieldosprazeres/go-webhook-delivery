package runtime

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeShutsDownAfterCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, server, listener, time.Second)
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve() did not stop after cancellation")
	}
}

func TestWaitReturnsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestShutdownBudgetUsesOneAbsoluteDeadlineAcrossPhases(t *testing.T) {
	budget := NewShutdownBudget(80 * time.Millisecond)
	started := time.Now()
	coreCtx, cancelCore := budget.Context(0)
	defer cancelCore()
	time.Sleep(40 * time.Millisecond)
	flushCtx, cancelFlush := budget.Context(time.Second)
	defer cancelFlush()
	coreDeadline, _ := coreCtx.Deadline()
	flushDeadline, _ := flushCtx.Deadline()
	if !coreDeadline.Equal(flushDeadline) {
		t.Fatalf("core deadline=%s flush deadline=%s", coreDeadline, flushDeadline)
	}
	<-flushCtx.Done()
	if elapsed := time.Since(started); elapsed < 70*time.Millisecond || elapsed > 160*time.Millisecond {
		t.Fatalf("shared deadline elapsed=%s", elapsed)
	}
	select {
	case <-coreCtx.Done():
	case <-time.After(20 * time.Millisecond):
		t.Fatal("core context did not expire at shared deadline")
	}
}
