package proxy

import "testing"

func TestServiceEndpoint(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:8787": "127.0.0.1:8787",
		"http://Proxy.LAN/path": "proxy.lan:80",
		"https://proxy.lan":     "proxy.lan:443",
		" http://[::1]:9000/ ":  "[::1]:9000",
	}
	for in, want := range cases {
		got, err := ServiceEndpoint(in)
		if err != nil || got != want {
			t.Fatalf("ServiceEndpoint(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "not a url", "/relative"} {
		if _, err := ServiceEndpoint(in); err == nil {
			t.Fatalf("ServiceEndpoint(%q) must fail", in)
		}
	}
}
