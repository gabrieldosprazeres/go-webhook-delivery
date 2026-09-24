// Command hmac-consumer is a minimal safe WDE webhook consumer.
package main

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/signing"
)

func main() {
	keyID := os.Getenv("WDE_SIGNING_KEY_ID")
	secret, err := base64.RawURLEncoding.Strict().DecodeString(os.Getenv("WDE_SIGNING_SECRET"))
	if err != nil || len(secret) != 32 || len(keyID) < 8 {
		_, _ = os.Stderr.WriteString("hmac-consumer: invalid signing configuration\n")
		os.Exit(1)
	}
	defer clear(secret)
	server := &http.Server{Addr: "127.0.0.1:8082", Handler: handler(keyID, secret),
		ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second,
		WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		_, _ = os.Stderr.WriteString("hmac-consumer: server failed\n")
		os.Exit(1)
	}
}

func handler(keyID string, secret []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get(signing.HeaderVersion) != "v1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		defer clear(body)
		err = signing.Verify(secret, time.Now(), 5*time.Minute,
			r.Header.Get(signing.HeaderTimestamp), r.Header.Get(signing.HeaderEventID),
			r.Header.Get(signing.HeaderDeliveryID), keyID, r.Header.Get(signing.HeaderSignature), body)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Persist and deduplicate by WDE-Delivery-ID before returning success.
		w.WriteHeader(http.StatusNoContent)
	})
}
