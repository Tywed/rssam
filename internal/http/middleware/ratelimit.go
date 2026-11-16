package middleware

import (
	"cmp"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	bucketTTL       = 5 * time.Minute
	cleanupInterval = 5 * time.Minute
	maxBuckets      = 100_000
	// evictTarget is the fill level a full table is cut back to, so the
	// O(n log n) eviction pays for the next 10 % of fresh keys at once
	// instead of running on every new key.
	evictTarget = maxBuckets * 9 / 10
)

// LimiterKey normalises a client address for rate limiting: IPv4 as is,
// IPv6 by its /64 prefix — one subscriber owns a whole /64 and could
// otherwise draw a fresh key for every request. Non-address keys pass
// through unchanged.
func LimiterKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "unknown"
	}
	a, err := netip.ParseAddr(strings.Trim(key, "[]"))
	if err != nil {
		return key
	}
	a = a.Unmap()
	if a.Is4() {
		return a.String()
	}
	return netip.PrefixFrom(a, 64).Masked().String()
}

// Limiter is an in-memory per-key token bucket rate limiter.
type Limiter struct {
	enabled bool
	rate    float64
	burst   float64

	mu          sync.Mutex
	buckets     map[string]*tokenBucket
	lastCleanup time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter creates a token-bucket limiter. rate is tokens per second; burst is max bucket size.
func NewLimiter(enabled bool, rate float64, burst int) *Limiter {
	if rate <= 0 {
		rate = 10
	}
	if burst <= 0 {
		burst = 20
	}
	return &Limiter{
		enabled: enabled,
		rate:    rate,
		burst:   float64(burst),
		buckets: make(map[string]*tokenBucket),
	}
}

func (l *Limiter) Enabled() bool {
	return l != nil && l.enabled
}

// Allow reports whether the key may proceed (consumes one token when allowed).
func (l *Limiter) Allow(key string) bool {
	if l == nil || !l.enabled {
		return true
	}
	key = LimiterKey(key)

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) >= maxBuckets || now.Sub(l.lastCleanup) >= cleanupInterval {
		l.evictStaleLocked(now)
		if len(l.buckets) >= maxBuckets {
			l.evictOldestLocked(len(l.buckets) - evictTarget)
		}
		l.lastCleanup = now
	}

	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Exhausted reports whether key has no token left, without consuming one.
func (l *Limiter) Exhausted(key string) bool {
	if l == nil || !l.enabled {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[LimiterKey(key)]
	if !ok {
		return false
	}
	return b.tokens+time.Since(b.last).Seconds()*l.rate < 1
}

// Len reports the number of tracked keys (tests and metrics).
func (l *Limiter) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *Limiter) evictStaleLocked(now time.Time) {
	cutoff := now.Add(-bucketTTL)
	for key, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// evictOldestLocked drops the n least recently seen keys in one sort pass.
func (l *Limiter) evictOldestLocked(n int) {
	if n <= 0 {
		return
	}
	type kv struct {
		key  string
		last int64
	}
	all := make([]kv, 0, len(l.buckets))
	for key, b := range l.buckets {
		all = append(all, kv{key, b.last.UnixNano()})
	}
	slices.SortFunc(all, func(a, b kv) int { return cmp.Compare(a.last, b.last) })
	for _, item := range all[:min(n, len(all))] {
		delete(l.buckets, item.key)
	}
}

// IsSensitiveRoute returns true for endpoints that should be rate limited per IP.
func IsSensitiveRoute(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := r.URL.Path
	if p == "/v1/me/api-keys" {
		return true
	}
	if strings.Contains(p, "/refresh") {
		return true
	}
	if strings.Contains(p, "/webhooks/") && strings.HasSuffix(p, "/test") {
		return true
	}
	if strings.HasPrefix(p, "/v1/entries/") && strings.HasSuffix(p, "/fetch-content") {
		return true
	}
	return false
}

// PerIPRateLimit applies per-IP rate limiting to every request on the wrapped handler.
func PerIPRateLimit(l *Limiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if l != nil && l.Enabled() && !l.Allow(ClientIP(r)) {
				WriteJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SensitiveRateLimit applies per-IP rate limiting to sensitive routes.
func SensitiveRateLimit(l *Limiter) func(http.Handler) http.Handler {
	inner := PerIPRateLimit(l)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if l != nil && l.Enabled() && IsSensitiveRoute(r) {
				inner(next).ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
