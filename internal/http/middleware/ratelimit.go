package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter is an in-memory per-key token bucket rate limiter.
type Limiter struct {
	enabled bool
	rate    float64
	burst   float64

	mu      sync.Mutex
	buckets map[string]*tokenBucket
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
	key = strings.TrimSpace(key)
	if key == "" {
		key = "unknown"
	}

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

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
