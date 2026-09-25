package ratelimit

import (
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/auth"
	"github.com/gabrieldosprazeres/go-webhook-delivery/internal/platform/problem"
)

type EdgePolicy struct {
	MaxInFlight                        int
	Global, Origin, Prefix, MaxBuckets int
	Window                             time.Duration
}

type edgeBucket struct {
	key         [32]byte
	windowStart int64
	count       int
}

type EdgeLimiter struct {
	policy EdgePolicy
	pepper [32]byte
	slots  chan struct{}
	mu     sync.Mutex
	global edgeBucket
	lru    *list.List
	items  map[[32]byte]*list.Element
	now    func() time.Time
}

func NewEdge(policy EdgePolicy, pepper [32]byte) *EdgeLimiter {
	return &EdgeLimiter{
		policy: policy, pepper: pepper, slots: make(chan struct{}, policy.MaxInFlight),
		lru: list.New(), items: make(map[[32]byte]*list.Element), now: time.Now,
	}
}

func (l *EdgeLimiter) Middleware(next http.Handler) http.Handler {
	return l.middleware(next, func(r *http.Request) string {
		prefix, _ := auth.PresentedPrefix(r.Header.Get("Authorization"))
		return prefix
	})
}

// LoginMiddleware bounds and parses the login form once so the API-key prefix
// participates in throttling without retaining or logging the raw credential.
func (l *EdgeLimiter) LoginMiddleware(next http.Handler) http.Handler {
	return l.middleware(next, func(r *http.Request) string {
		r.Body = http.MaxBytesReader(nil, r.Body, 8<<10)
		if err := r.ParseForm(); err != nil {
			return ""
		}
		prefix, _ := auth.PresentedTokenPrefix(r.FormValue("api_key"))
		return prefix
	})
}

func (l *EdgeLimiter) middleware(next http.Handler, prefixOf func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case l.slots <- struct{}{}:
			defer func() { <-l.slots }()
		default:
			writeEdgeLimit(w, r, 1)
			return
		}
		allowed, retryAfter := l.allow(requestOrigin(r), prefixOf(r))
		if !allowed {
			writeEdgeLimit(w, r, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *EdgeLimiter) allow(origin, prefix string) (bool, int) {
	now := l.now()
	windowSeconds := int64(l.policy.Window / time.Second)
	windowStart := now.Unix() / windowSeconds * windowSeconds
	type requestedBucket struct {
		key   [32]byte
		limit int
	}
	keys := []requestedBucket{{l.localKey("origin", origin), l.policy.Origin}}
	if prefix != "" {
		keys = append(keys, requestedBucket{l.localKey("prefix", prefix), l.policy.Prefix})
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !bucketAvailable(&l.global, windowStart, l.policy.Global) {
		return false, retrySeconds(now, windowStart, windowSeconds)
	}
	buckets := make([]*edgeBucket, 0, len(keys))
	for _, item := range keys {
		bucket := l.bucket(item.key, windowStart)
		if !bucketAvailable(bucket, windowStart, item.limit) {
			return false, retrySeconds(now, windowStart, windowSeconds)
		}
		buckets = append(buckets, bucket)
	}
	l.global.count++
	for _, bucket := range buckets {
		bucket.count++
	}
	return true, 0
}

func (l *EdgeLimiter) bucket(key [32]byte, windowStart int64) *edgeBucket {
	if element := l.items[key]; element != nil {
		l.lru.MoveToFront(element)
		bucket := element.Value.(*edgeBucket)
		resetBucket(bucket, windowStart)
		return bucket
	}
	if l.lru.Len() >= l.policy.MaxBuckets {
		oldest := l.lru.Back()
		delete(l.items, oldest.Value.(*edgeBucket).key)
		l.lru.Remove(oldest)
	}
	bucket := &edgeBucket{key: key, windowStart: windowStart}
	l.items[key] = l.lru.PushFront(bucket)
	return bucket
}

func (l *EdgeLimiter) localKey(kind, value string) [32]byte {
	mac := hmac.New(sha256.New, l.pepper[:])
	_, _ = mac.Write([]byte("edge-limit:v1\n" + kind + "\n" + value))
	var result [32]byte
	copy(result[:], mac.Sum(nil))
	return result
}

func bucketAvailable(bucket *edgeBucket, windowStart int64, limit int) bool {
	resetBucket(bucket, windowStart)
	return bucket.count < limit
}

func resetBucket(bucket *edgeBucket, windowStart int64) {
	if bucket.windowStart != windowStart {
		bucket.windowStart, bucket.count = windowStart, 0
	}
}

func retrySeconds(now time.Time, start, window int64) int {
	retry := int(start + window - now.Unix())
	if retry < 1 {
		return 1
	}
	return retry
}

func requestOrigin(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(r.RemoteAddr) != nil {
		return r.RemoteAddr
	}
	return "unknown"
}

func writeEdgeLimit(w http.ResponseWriter, r *http.Request, retryAfter int) {
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	problem.Write(w, r, http.StatusTooManyRequests, "quota_exceeded", "Quota exceeded")
}
