package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

type verification struct {
	keyID  string
	secret []byte
}

type deliveryResult struct {
	duration                      time.Duration
	unique, duplicates, missing   int
	unexpected, invalidSignatures int
}

type receiver struct {
	mu                     sync.Mutex
	cond                   *sync.Cond
	verification           verification
	expected, seen         map[string]struct{}
	duplicates, unexpected int
	invalidSignatures      int
	first, last            time.Time
	done                   chan struct{}
	completed, accepting   bool
	activeHandlers         int
}

func newReceiver() *receiver {
	receiver := &receiver{done: make(chan struct{}), accepting: true}
	receiver.cond = sync.NewCond(&receiver.mu)
	return receiver
}

func (r *receiver) expect(ids []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(ids) == 0 || r.expected != nil {
		return errors.New("invalid expected delivery set")
	}
	r.expected = make(map[string]struct{}, len(ids))
	r.seen = make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return errors.New("invalid expected delivery ID")
		}
		if _, exists := r.expected[id]; exists {
			return errors.New("duplicate expected delivery ID")
		}
		r.expected[id] = struct{}{}
	}
	return nil
}

func (r *receiver) configure(keyID string, secret []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.verification.secret)
	r.verification = verification{keyID: keyID, secret: append([]byte(nil), secret...)}
}

func (r *receiver) clearSecret() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.verification.secret)
	r.verification = verification{}
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if !r.beginHandler() {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	defer r.endHandler()
	if request.Method != http.MethodPost || request.URL.Path != "/webhook" {
		http.NotFound(w, request)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	defer clear(body)
	r.mu.Lock()
	defer r.mu.Unlock()
	v := r.verification
	if signing.Verify(v.secret, time.Now(), 5*time.Minute, request.Header.Get(signing.HeaderTimestamp),
		request.Header.Get(signing.HeaderEventID), request.Header.Get(signing.HeaderDeliveryID),
		v.keyID, request.Header.Get(signing.HeaderSignature), body) != nil {
		r.invalidSignatures++
		http.Error(w, "invalid", http.StatusUnauthorized)
		return
	}
	now := time.Now()
	deliveryID := request.Header.Get(signing.HeaderDeliveryID)
	if _, expected := r.expected[deliveryID]; !expected {
		r.unexpected++
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if _, duplicate := r.seen[deliveryID]; duplicate {
		r.duplicates++
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(r.seen) == 0 {
		r.first = now
	}
	r.last = now
	r.seen[deliveryID] = struct{}{}
	if len(r.seen) == len(r.expected) {
		r.finishLocked()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *receiver) finishLocked() {
	if !r.completed {
		r.completed = true
		close(r.done)
	}
}

func (r *receiver) waitAll(ctx context.Context) error {
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *receiver) quiesce() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accepting = false
	for r.activeHandlers > 0 {
		r.cond.Wait()
	}
}

func (r *receiver) snapshot() (deliveryResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := deliveryResult{duration: r.last.Sub(r.first), unique: len(r.seen), duplicates: r.duplicates,
		missing: len(r.expected) - len(r.seen), unexpected: r.unexpected,
		invalidSignatures: r.invalidSignatures}
	if r.accepting || r.activeHandlers != 0 {
		return result, errors.New("receiver snapshot requires quiescence")
	}
	if result.invalidSignatures != 0 || result.missing != 0 || result.unexpected != 0 {
		return result, errors.New("invalid or missing signed deliveries")
	}
	return result, nil
}

func (r *receiver) beginHandler() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.accepting {
		return false
	}
	r.activeHandlers++
	return true
}

func (r *receiver) endHandler() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeHandlers--
	if r.activeHandlers == 0 {
		r.cond.Broadcast()
	}
}
