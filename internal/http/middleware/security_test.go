package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	h := SecurityHeaders(SecurityConfig{HSTSEnabled: true})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/feeds", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	checks := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "no-referrer",
		"Content-Security-Policy":   "default-src 'none'; frame-ancestors 'none'",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, want := range checks {
		if got := rec.Header().Get(k); got != want {
			t.Fatalf("%s=%q, want %q", k, got, want)
		}
	}
	if got := rec.Header().Get("Permissions-Policy"); got == "" {
		t.Fatal("expected Permissions-Policy header")
	}
}

// UI pages: Referrer-Policy same-origin, so a plain-http LAN install
// (no Sec-Fetch-* from the browser) still passes SameOriginRequest by
// Referer; the API keeps no-referrer.
func TestSecurityHeaders_UIReferrerPolicy(t *testing.T) {
	h := SecurityHeaders(SecurityConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	for path, want := range map[string]string{"/ui/feeds": "same-origin", "/ui": "same-origin", "/v1/feeds": "no-referrer", "/healthz": "no-referrer"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get("Referrer-Policy"); got != want {
			t.Fatalf("%s: Referrer-Policy=%q, want %q", path, got, want)
		}
	}
	// What such a browser sends on a same-origin form POST: no
	// Sec-Fetch-Site, Origin: null, Referer of our own page.
	req := httptest.NewRequest(http.MethodPost, "http://192.168.0.5:8080/ui/feeds", nil)
	req.Host = "192.168.0.5:8080"
	req.Header.Set("Origin", "null")
	if SameOriginRequest(req) {
		t.Fatal("Origin: null without Referer must be rejected")
	}
	req.Header.Set("Referer", "http://192.168.0.5:8080/ui/feeds/new")
	if SameOriginRequest(req) {
		t.Fatal("Origin: null must not be rescued by Referer: Origin is checked first")
	}
	req.Header.Del("Origin")
	if !SameOriginRequest(req) {
		t.Fatal("same-origin Referer alone must pass")
	}
}

func TestSecurityHeaders_HSTSDisabled(t *testing.T) {
	h := SecurityHeaders(SecurityConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HSTS should be absent, got %q", got)
	}
}
