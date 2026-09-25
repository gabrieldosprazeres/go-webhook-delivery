// Package ratelimit enforces cluster-wide fixed-window quotas in PostgreSQL.
package ratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/tenanttx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrExceeded = errors.New("ratelimit: quota exceeded")

type Policy struct {
	Operation                           string
	Global, Workspace, APIKey, Resource int
	Window                              time.Duration
}

type Limiter struct {
	pool   *pgxpool.Pool
	pepper [32]byte
}

type dimension struct {
	typeName                     string
	workspaceID, apiKeyID, value uuid.UUID
	limit                        int
}

type exceededError struct{ retryAfter int }

func (e exceededError) Error() string { return ErrExceeded.Error() }
func (e exceededError) Unwrap() error { return ErrExceeded }

// RetryAfter returns the bounded retry delay carried by a quota error.
func RetryAfter(err error) (int, bool) {
	var exceeded exceededError
	if !errors.As(err, &exceeded) {
		return 0, false
	}
	return exceeded.retryAfter, true
}

func New(pool *pgxpool.Pool, pepper [32]byte) *Limiter {
	return &Limiter{pool: pool, pepper: pepper}
}

func (l *Limiter) Middleware(policy Policy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFrom(r.Context())
		if !ok {
			problem.Write(w, r, http.StatusUnauthorized, "unauthorized", "Authentication required")
			return
		}
		resourceID, _ := uuid.Parse(r.PathValue("id"))
		err := l.Consume(r.Context(), principal, resourceID, policy)
		var exceeded exceededError
		if errors.As(err, &exceeded) {
			w.Header().Set("Retry-After", strconv.Itoa(exceeded.retryAfter))
			problem.Write(w, r, http.StatusTooManyRequests, "quota_exceeded", "Quota exceeded")
			return
		}
		if err != nil {
			problem.Write(w, r, http.StatusServiceUnavailable, "quota_unavailable", "Quota service unavailable")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *Limiter) Consume(ctx context.Context, principal auth.Principal, resourceID uuid.UUID, policy Policy) error {
	if principal.WorkspaceID == uuid.Nil || principal.APIKeyID == uuid.Nil ||
		policy.Window < time.Second || policy.Window > time.Hour || policy.Window%time.Second != 0 {
		return errors.New("ratelimit: invalid policy")
	}
	dimensions := []dimension{
		{typeName: "global", limit: policy.Global},
		{typeName: "workspace", workspaceID: principal.WorkspaceID, value: principal.WorkspaceID, limit: policy.Workspace},
		{typeName: "api_key", workspaceID: principal.WorkspaceID, apiKeyID: principal.APIKeyID, value: principal.APIKeyID, limit: policy.APIKey},
	}
	if policy.Resource > 0 && resourceID != uuid.Nil {
		dimensions = append(dimensions, dimension{typeName: "delivery", workspaceID: principal.WorkspaceID,
			value: resourceID, limit: policy.Resource})
	}
	return tenanttx.Within(ctx, l.pool, principal.WorkspaceID, func(tx pgx.Tx) error {
		for _, item := range dimensions {
			if item.limit < 1 {
				return errors.New("ratelimit: invalid limit")
			}
			allowed, retryAfter, err := l.consumeDimension(ctx, tx, policy, item)
			if err != nil {
				return err
			}
			if !allowed {
				return exceededError{retryAfter: retryAfter}
			}
		}
		return nil
	})
}

func (l *Limiter) consumeDimension(ctx context.Context, tx pgx.Tx, policy Policy, item dimension) (bool, int, error) {
	var allowed bool
	var retryAfter int
	var workspaceID, apiKeyID, resourceID any
	if item.workspaceID != uuid.Nil {
		workspaceID = item.workspaceID
	}
	if item.apiKeyID != uuid.Nil {
		apiKeyID = item.apiKeyID
	}
	if item.typeName == "delivery" {
		resourceID = item.value
	}
	err := tx.QueryRow(ctx, `SELECT allowed,retry_after_seconds FROM wde.consume_quota($1,$2,$3,$4,$5,$6,$7,$8)`,
		dimensionDigest(l.pepper, policy, item), item.typeName, policy.Operation, workspaceID, apiKeyID, resourceID,
		item.limit, int(policy.Window/time.Second)).Scan(&allowed, &retryAfter)
	return allowed, retryAfter, err
}

func dimensionDigest(pepper [32]byte, policy Policy, item dimension) []byte {
	mac := hmac.New(sha256.New, pepper[:])
	_, _ = mac.Write([]byte("quota:v2\n" + item.typeName + "\n" + policy.Operation + "\n"))
	_, _ = mac.Write(item.workspaceID[:])
	_, _ = mac.Write(item.apiKeyID[:])
	_, _ = mac.Write(item.value[:])
	var window [8]byte
	binary.BigEndian.PutUint64(window[:], uint64(policy.Window/time.Second))
	_, _ = mac.Write(window[:])
	return mac.Sum(nil)
}
