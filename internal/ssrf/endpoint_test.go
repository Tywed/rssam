package ssrf

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientForEndpoint_OnlyThatHostPort(t *testing.T) {
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://10.0.0.1:1/", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(svc.Close)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(other.Close)

	guard, err := New(Config{LookupHost: publicLookup(t)})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(svc.URL)
	endpoint := u.Host
	c := guard.HTTPClientForEndpoint(2*time.Second, endpoint)

	resp, err := c.Get(svc.URL + "/ok")
	if err != nil {
		t.Fatalf("exempted endpoint must be reachable: %v", err)
	}
	resp.Body.Close()

	resp, err = c.Get(svc.URL + "/redirect")
	if err != nil {
		t.Fatalf("redirect must be returned, not followed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status=%d, want 302 (redirects not followed)", resp.StatusCode)
	}

	if resp, err := c.Get(other.URL); err == nil {
		resp.Body.Close()
		t.Fatal("same host, other port must be refused")
	} else if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got %v", err)
	}

	host, port, _ := net.SplitHostPort(endpoint)
	if host != "127.0.0.1" {
		t.Skipf("httptest bound to %s", host)
	}
	if resp, err := c.Get("http://localhost:" + port + "/ok"); err == nil {
		resp.Body.Close()
		t.Fatal("another name for the same address must be refused")
	}

	if resp, err := guard.HTTPClient(2 * time.Second).Get(svc.URL + "/ok"); err == nil {
		resp.Body.Close()
		t.Fatal("ordinary client must not inherit the exemption")
	}
	if err := guard.ValidateURL(svc.URL); err == nil {
		t.Fatal("ValidateURL must refuse the endpoint for feeds/webhooks")
	}
}

func TestHTTPClientForEndpoint_BadEndpointFallsBack(t *testing.T) {
	guard, err := New(Config{LookupHost: publicLookup(t)})
	if err != nil {
		t.Fatal(err)
	}
	if c := guard.HTTPClientForEndpoint(time.Second, "no-port"); c == nil {
		t.Fatal("nil client")
	}
}
