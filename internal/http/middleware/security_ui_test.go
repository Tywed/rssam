package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders_UIRoute(t *testing.T) {
	h := SecurityHeaders(SecurityConfig{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("UI route should have relaxed CSP, got %q", csp)
	}
	if csp == "" || !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("unexpected CSP: %q", csp)
	}
}
