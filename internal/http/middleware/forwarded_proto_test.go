package middleware

import (
	"crypto/tls"
	"testing"
)

// A direct client must not be able to declare its own plain-HTTP connection
// "secure": the session cookie would get the Secure flag and be dropped by
// the browser (login loop), and any "is HTTPS" logic would lie.
func TestRequestIsSecure_IgnoresForwardedProtoFromUntrustedPeer(t *testing.T) {
	withProxies(t, "127.0.0.0/8")
	r := req("203.0.113.9:4444", map[string]string{"X-Forwarded-Proto": "https"})
	if RequestIsSecure(r, false) {
		t.Fatal("X-Forwarded-Proto from an untrusted peer must be ignored")
	}
	if got := ForwardedProto(r); got != "" {
		t.Fatalf("ForwardedProto=%q, want empty", got)
	}
}

func TestRequestIsSecure_HonoursForwardedProtoFromTrustedProxy(t *testing.T) {
	withProxies(t, "127.0.0.0/8", "10.0.0.0/8")
	cases := []struct {
		remote string
		proto  string
		want   bool
	}{
		{"127.0.0.1:5555", "https", true},
		{"10.1.2.3:5555", "HTTPS", true},
		{"127.0.0.1:5555", "https, http", true}, // first hop is client-facing
		{"127.0.0.1:5555", "http", false},
		{"127.0.0.1:5555", "", false},
		{"127.0.0.1:5555", "https-ish", false},
	}
	for _, tc := range cases {
		hdr := map[string]string{}
		if tc.proto != "" {
			hdr["X-Forwarded-Proto"] = tc.proto
		}
		if got := RequestIsSecure(req(tc.remote, hdr), false); got != tc.want {
			t.Errorf("remote=%s proto=%q: secure=%v, want %v", tc.remote, tc.proto, got, tc.want)
		}
	}
}

func TestRequestIsSecure_TLSAndHSTS(t *testing.T) {
	withProxies(t) // no trusted proxies at all
	r := req("203.0.113.9:4444", nil)
	if RequestIsSecure(r, false) {
		t.Fatal("plain request must not be secure")
	}
	if !RequestIsSecure(r, true) {
		t.Fatal("HSTS deployments are HTTPS-only at the edge")
	}
	r.TLS = &tls.ConnectionState{}
	if !RequestIsSecure(r, false) {
		t.Fatal("direct TLS must be secure")
	}
}

func TestPeerIsTrustedProxy(t *testing.T) {
	withProxies(t, "::1/128", "192.0.2.0/24")
	if !PeerIsTrustedProxy(req("[::1]:1", nil)) || !PeerIsTrustedProxy(req("192.0.2.77:1", nil)) {
		t.Fatal("listed peers must be trusted")
	}
	if PeerIsTrustedProxy(req("192.0.3.1:1", nil)) || PeerIsTrustedProxy(req("garbage", nil)) {
		t.Fatal("unlisted / unparsable peers must not be trusted")
	}
}
