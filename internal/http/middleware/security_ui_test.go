package middleware

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var cspNonceRe = regexp.MustCompile(`'nonce-([A-Za-z0-9_-]+)'`)

func TestSecurityHeaders_UIRoute(t *testing.T) {
	var seen string
	h := SecurityHeaders(SecurityConfig{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = CSPNonce(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	serve := func() string {
		req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Header().Get("Content-Security-Policy")
	}

	csp := serve()
	if csp == apiCSP {
		t.Fatalf("UI route should have relaxed CSP, got %q", csp)
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("unexpected CSP: %q", csp)
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("CSP must not allow unsafe-inline: %q", csp)
	}
	m := cspNonceRe.FindAllStringSubmatch(csp, -1)
	if len(m) != 2 || m[0][1] != m[1][1] {
		t.Fatalf("expected the same nonce in script-src and style-src: %q", csp)
	}
	if seen == "" || seen != m[0][1] {
		t.Fatalf("context nonce %q, header nonce %q", seen, m[0][1])
	}
	if len(seen) < 22 {
		t.Fatalf("nonce too short: %q", seen)
	}

	if again := serve(); again == csp {
		t.Fatal("nonce must differ between requests")
	}
}

func TestCSPNonce_OutsideMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	if got := CSPNonce(req.Context()); got != "" {
		t.Fatalf("nonce outside middleware = %q, want empty", got)
	}
}
