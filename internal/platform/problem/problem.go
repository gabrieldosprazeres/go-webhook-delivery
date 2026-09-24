// Package problem writes stable RFC 9457-style HTTP problem details.
package problem

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

const mediaType = "application/problem+json"

type requestIDContextKey struct{}

// Details is the public, sanitized representation of an HTTP error.
type Details struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
}

// Write sends a problem response without infrastructure details.
func Write(w http.ResponseWriter, r *http.Request, status int, code, title string) {
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Details{
		Type:      "about:blank",
		Title:     title,
		Status:    status,
		Code:      code,
		RequestID: RequestID(r.Context()),
	})
}

// NotFound returns a stable response that does not disclose route internals.
func NotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Write(w, r, http.StatusNotFound, "not_found", "Resource not found")
	})
}

// WithRequestID generates a server-owned request identifier for every request.
func WithRequestID(next http.Handler) http.Handler {
	return withRequestIDGenerator(next, newRequestID)
}

func withRequestIDGenerator(next http.Handler, generate func() (string, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, err := generate()
		if err != nil {
			requestID = "unavailable"
		}
		w.Header().Set("X-Request-ID", requestID)
		ctx := context.WithValue(r.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestID returns the server-owned request identifier or an empty string.
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

func newRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "req_" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
