// Package runtime coordinates process cancellation and graceful HTTP shutdown.
package runtime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// HTTPServer couples an HTTP server with its already-bound listener.
type HTTPServer struct {
	Server   *http.Server
	Listener net.Listener
}

// SignalContext is cancelled by SIGINT or SIGTERM.
func SignalContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// Serve runs an HTTP server until it fails or its context is cancelled.
func Serve(ctx context.Context, server *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	return ServeWithBudget(ctx, server, listener, NewShutdownBudget(shutdownTimeout))
}

// ServeWithBudget runs one server and consumes a shared shutdown deadline.
func ServeWithBudget(ctx context.Context, server *http.Server, listener net.Listener, budget *ShutdownBudget) error {
	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()

	select {
	case err := <-serveErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		budget.Start()
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := budget.Context(0)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		err := <-serveErrors
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// ServeAll owns multiple HTTP servers and stops every listener when one exits.
func ServeAll(ctx context.Context, shutdownTimeout time.Duration, servers ...HTTPServer) error {
	return ServeAllWithBudget(ctx, NewShutdownBudget(shutdownTimeout), servers...)
}

// ServeAllWithBudget stops all servers against the same absolute deadline.
func ServeAllWithBudget(ctx context.Context, budget *ShutdownBudget, servers ...HTTPServer) error {
	if len(servers) == 0 {
		return nil
	}
	groupCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errorsCh := make(chan error, len(servers))
	for _, item := range servers {
		go func() {
			errorsCh <- ServeWithBudget(groupCtx, item.Server, item.Listener, budget)
		}()
	}

	var result error
	for range servers {
		err := <-errorsCh
		if err != nil {
			result = errors.Join(result, err)
		}
		cancel()
	}
	return result
}

// Wait blocks a process without listeners until cancellation.
func Wait(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
