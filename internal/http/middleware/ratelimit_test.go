package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiter_Returns429AfterBurst(t *testing.T) {
	limiter := NewLimiter(true, 1000, 2)
	handler := SensitiveRateLimit(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/v1/feeds/1/refresh", nil)
		req.RemoteAddr = "203.0.113.1:12345"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := do(); code != http.StatusOK {
		t.Fatalf("first request: status=%d", code)
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("second request: status=%d", code)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/1/refresh", nil)
	req.RemoteAddr = "203.0.113.1:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("third request: status=%d, want 429", rec.Code)
	}
	var body struct {
		ErrorMessage string `json:"error_message"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ErrorMessage == "" {
		t.Fatal("expected error_message in 429 body")
	}
}

func TestLimiter_EvictsStaleBuckets(t *testing.T) {
	limiter := NewLimiter(true, 10, 20)
	if !limiter.Allow("stale") {
		t.Fatal("expected allow")
	}
	limiter.mu.Lock()
	limiter.buckets["stale"].last = time.Now().Add(-10 * time.Minute)
	limiter.lastCleanup = time.Now().Add(-cleanupInterval - time.Second)
	limiter.mu.Unlock()

	if !limiter.Allow("fresh") {
		t.Fatal("expected allow fresh")
	}
	limiter.mu.Lock()
	_, stale := limiter.buckets["stale"]
	_, fresh := limiter.buckets["fresh"]
	limiter.mu.Unlock()
	if stale {
		t.Fatal("stale bucket was not evicted")
	}
	if !fresh {
		t.Fatal("fresh bucket missing")
	}
}

func TestIsSensitiveRoute(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{http.MethodPost, "/v1/me/api-keys", true},
		{http.MethodPost, "/v1/feeds/1/refresh", true},
		{http.MethodPost, "/v1/webhooks/2/test", true},
		{http.MethodGet, "/v1/feeds/1/refresh", false},
		{http.MethodPost, "/v1/feeds", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := IsSensitiveRoute(r); got != tc.want {
			t.Fatalf("%s %s: got %v want %v", tc.method, tc.path, got, tc.want)
		}
	}
}
