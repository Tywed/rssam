package ssrf

import "testing"

// The HTTP(S)_PROXY exemption names an endpoint, not a host: the same
// address on another port goes through the guard like any feed URL.
func TestProxyEndpoint(t *testing.T) {
	for raw, want := range map[string]string{
		"http://127.0.0.1:3128":   "127.0.0.1:3128",
		"http://Proxy.Local":      "proxy.local:80",
		"https://proxy.local":     "proxy.local:443",
		"socks5://[::1]:9050":     "[::1]:9050",
		"socks5h://tor":           "tor:1080",
		"http://user:pw@p:8080/x": "p:8080",
		"://bad":                  "",
	} {
		if got := proxyEndpoint(raw); got != want {
			t.Errorf("%s: got %q, want %q", raw, got, want)
		}
	}
	if _, ok := proxyEndpoints["127.0.0.1:5432"]; ok {
		t.Fatal("no proxy configured in tests, nothing may be exempt")
	}
}
