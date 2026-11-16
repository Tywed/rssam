package ssrf

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// publicLookup maps *.public to a routable address so the guard accepts the
// name while the real connection is still made to the local test server.
func publicLookup(t *testing.T) func(context.Context, string) ([]string, error) {
	t.Helper()
	return func(_ context.Context, host string) ([]string, error) {
		switch {
		case strings.HasSuffix(host, ".public"):
			return []string{"93.184.216.34"}, nil
		case host == "127.0.0.1" || host == "localhost":
			return []string{"127.0.0.1"}, nil
		}
		return nil, fmt.Errorf("no such host %q", host)
	}
}

func TestHTTPClient_RedirectToPrivateHostBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1:1/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	guard, err := New(Config{AllowPrivateNetwork: true, LookupHost: publicLookup(t)})
	if err != nil {
		t.Fatal(err)
	}
	guard.allowPrivate = false
	resp, err := guard.HTTPClient(2 * time.Second).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected request to a private address to fail")
	}
	if !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("expected ssrf error, got %v", err)
	}
}

func TestHTTPClient_RedirectLimit(t *testing.T) {
	var hops int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, fmt.Sprintf("/%d", hops), http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	guard, err := New(Config{AllowPrivateNetwork: true, LookupHost: publicLookup(t)})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := guard.HTTPClient(2 * time.Second).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect loop to fail")
	}
	if !strings.Contains(err.Error(), "stopped after 5 redirects") {
		t.Fatalf("unexpected error %v", err)
	}
	if hops != redirectLimit {
		t.Fatalf("hops=%d want %d", hops, redirectLimit)
	}
}

func TestHTTPClientNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	guard, err := New(Config{AllowPrivateNetwork: true, LookupHost: publicLookup(t)})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := guard.HTTPClientNoRedirect(2*time.Second).Post(srv.URL, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status=%d want 307 returned as-is", resp.StatusCode)
	}
}

// proxyServer is a minimal HTTP forward proxy on 127.0.0.1 that records the
// absolute URLs it is asked for and answers them itself.
func proxyServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.String())
		switch r.URL.Path {
		case "/redirect-private":
			http.Redirect(w, r, "http://127.0.0.1:1/secret", http.StatusFound)
		case "/redirect-public":
			http.Redirect(w, r, "http://b.public/final", http.StatusFound)
		default:
			_, _ = io.WriteString(w, "via proxy "+r.Host)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestHTTPClientViaProxy(t *testing.T) {
	proxy, seen := proxyServer(t)
	guard, err := New(Config{LookupHost: publicLookup(t)})
	if err != nil {
		t.Fatal(err)
	}
	client, err := guard.HTTPClientViaProxy(2*time.Second, false, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	// The proxy on 127.0.0.1 is dialled although private networks are blocked.
	resp, err := client.Get("http://a.public/redirect-public")
	if err != nil {
		t.Fatalf("fetch via proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "via proxy b.public" {
		t.Fatalf("body=%q", body)
	}
	if len(*seen) != 2 || (*seen)[1] != "http://b.public/final" {
		t.Fatalf("proxy saw %v", *seen)
	}

	// A target the guard rejects never reaches the proxy, neither directly...
	*seen = nil
	if resp, err := client.Get("http://127.0.0.1:1/secret"); err == nil {
		resp.Body.Close()
		t.Fatal("expected private target to be rejected")
	}
	if len(*seen) != 0 {
		t.Fatalf("proxy was asked for %v", *seen)
	}
	// ...nor through a redirect.
	if resp, err := client.Get("http://a.public/redirect-private"); err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect to a private target to be rejected")
	}
	if len(*seen) != 1 {
		t.Fatalf("proxy saw %v", *seen)
	}

	if _, err := guard.HTTPClientViaProxy(2*time.Second, false, "://bad"); err == nil {
		t.Fatal("expected invalid proxy URL error")
	}
}
