package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func withProxies(t *testing.T, entries ...string) {
	t.Helper()
	prev := TrustedProxies()
	if err := SetTrustedProxies(entries); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		strs := make([]string, 0, len(prev))
		for _, p := range prev {
			strs = append(strs, p.String())
		}
		_ = SetTrustedProxies(strs)
	})
}

func req(remote string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

// Regression: a remote client could rotate X-Forwarded-For and get a fresh
// rate-limit bucket on every request (verified 30/30 logins → 200).
func TestClientIP_IgnoresForwardedHeadersFromUntrustedPeer(t *testing.T) {
	withProxies(t, "127.0.0.0/8")
	r := req("203.0.113.9:4444", map[string]string{"X-Forwarded-For": "10.1.1.1", "X-Real-IP": "10.2.2.2"})
	if got := ClientIP(r); got != "203.0.113.9" {
		t.Fatalf("got %q, want peer address", got)
	}
}

func TestClientIP_TrustedProxyUsesRightmostUntrustedHop(t *testing.T) {
	withProxies(t, "127.0.0.0/8", "10.0.0.0/8")
	// client → 10.0.0.5 (trusted) → 127.0.0.1 (trusted) → app
	r := req("127.0.0.1:5555", map[string]string{"X-Forwarded-For": "1.1.1.1, 198.51.100.7, 10.0.0.5"})
	if got := ClientIP(r); got != "198.51.100.7" {
		t.Fatalf("got %q, want 198.51.100.7 (attacker-supplied 1.1.1.1 must be ignored)", got)
	}
}

func TestClientIP_TrustedProxyFallsBackToXRealIP(t *testing.T) {
	withProxies(t, "127.0.0.1")
	r := req("127.0.0.1:5555", map[string]string{"X-Real-IP": "198.51.100.8"})
	if got := ClientIP(r); got != "198.51.100.8" {
		t.Fatalf("got %q", got)
	}
}

func TestClientIP_NoProxiesConfiguredNeverTrustsHeaders(t *testing.T) {
	withProxies(t)
	r := req("127.0.0.1:5555", map[string]string{"X-Forwarded-For": "198.51.100.8"})
	if got := ClientIP(r); got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
}

func TestClientIP_MalformedChainStopsTrust(t *testing.T) {
	withProxies(t, "127.0.0.1")
	r := req("127.0.0.1:5555", map[string]string{"X-Forwarded-For": "garbage, 127.0.0.1"})
	if got := ClientIP(r); got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
}

func TestClientIP_IPv6MappedNormalised(t *testing.T) {
	withProxies(t, "::1")
	r := req("[::1]:5555", map[string]string{"X-Forwarded-For": "::ffff:198.51.100.9"})
	if got := ClientIP(r); got != "198.51.100.9" {
		t.Fatalf("got %q", got)
	}
}

func TestSetTrustedProxies_Invalid(t *testing.T) {
	if err := SetTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected error")
	}
}
