package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Why sameOriginRequest is not net/http.CrossOriginProtection (Go 1.25+).
//
// The stdlib check was evaluated as a replacement and rejected for three
// reasons that this test pins, so the decision is re-examined automatically
// if the stdlib behaviour changes:
//
//  1. It always allows GET, so it cannot protect the WebSocket handshake
//     (cross-site WebSocket hijacking is a GET with the session cookie).
//  2. Without Sec-Fetch-Site AND Origin it fails open; rssam rejects a
//     cookie-only mutation that carries no origin evidence at all.
//  3. It compares Origin with r.Host only, so behind a proxy that rewrites
//     Host (nginx proxy_pass default) old browsers without Sec-Fetch-Site are
//     rejected unless the operator configures AddTrustedOrigin; rssam uses
//     X-Forwarded-Host from a trusted proxy instead (RequestHost).
//
// Where both apply (Sec-Fetch-Site present, or Origin vs Host) the verdicts
// agree, which the last block checks.
func TestSameOriginRequest_DeltaToStdlib(t *testing.T) {
	std := http.NewCrossOriginProtection()
	mk := func(method string, h map[string]string) *http.Request {
		r := httptest.NewRequest(method, "http://rss.example.com/v1/x", nil)
		r.RemoteAddr = "203.0.113.5:4444"
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}

	// 1. GET is never checked by the stdlib.
	ws := mk(http.MethodGet, map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"})
	if std.Check(ws) != nil {
		t.Fatal("stdlib now checks GET; revisit using it for the WebSocket handshake")
	}
	if sameOriginRequest(ws) {
		t.Fatal("rssam must reject a cross-site GET handshake")
	}

	// 2. No evidence at all.
	bare := mk(http.MethodPost, nil)
	if std.Check(bare) != nil {
		t.Fatal("stdlib now rejects header-less mutations; the local check may be redundant")
	}
	if sameOriginRequest(bare) {
		t.Fatal("rssam must reject a cookie-only mutation without origin evidence")
	}

	// 3. Host rewritten by a trusted proxy, old browser (no Sec-Fetch-Site).
	proxied := mk(http.MethodPost, map[string]string{"Origin": "https://rss.example.com", "X-Forwarded-Host": "rss.example.com"})
	proxied.Host = "127.0.0.1:8080"
	proxied.RemoteAddr = "127.0.0.1:5555"
	if std.Check(proxied) == nil {
		t.Fatal("stdlib now honours X-Forwarded-Host; revisit")
	}
	if !sameOriginRequest(proxied) {
		t.Fatal("rssam must accept the proxied same-origin request")
	}

	// Agreement on the common cases.
	agree := []map[string]string{
		{"Sec-Fetch-Site": "same-origin"},
		{"Sec-Fetch-Site": "none"},
		{"Sec-Fetch-Site": "cross-site", "Origin": "http://rss.example.com"},
		{"Sec-Fetch-Site": "same-site"},
		{"Origin": "http://rss.example.com"},
		{"Origin": "https://evil.example"},
		{"Origin": "null"},
	}
	for _, h := range agree {
		r := mk(http.MethodPost, h)
		if (std.Check(r) == nil) != sameOriginRequest(r) {
			t.Errorf("verdicts differ for %v: stdlib=%v local=%v", h, std.Check(r), sameOriginRequest(r))
		}
	}
}
