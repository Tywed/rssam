package ssrf

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientForFetch_PerFeedTLSInsecure(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	lookup := func(ctx context.Context, host string) ([]string, error) {
		return []string{"127.0.0.1"}, nil
	}
	guard, err := New(Config{
		AllowPrivateNetwork: true,
		LookupHost:          lookup,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	strictClient := guard.HTTPClientForFetch(2*time.Second, false)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := strictClient.Do(req); err == nil {
		t.Fatal("expected TLS verify error with per-feed tls_insecure=false")
	}

	perFeedClient := guard.HTTPClientForFetch(2*time.Second, true)
	transport, ok := perFeedClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected per-feed transport TLSClientConfig.InsecureSkipVerify=true")
	}

	req2, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := perFeedClient.Do(req2)
	if err != nil {
		t.Fatalf("per-feed insecure fetch: %v", err)
	}
	defer resp.Body.Close()
}

func TestHTTPClient_TLSInsecureSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	guard, err := New(Config{
		AllowPrivateNetwork: true,
		LookupHost: func(ctx context.Context, host string) ([]string, error) {
			return []string{"127.0.0.1"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	strictClient := guard.HTTPClient(2 * time.Second)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if _, err := strictClient.Do(req); err == nil {
		t.Fatal("expected TLS verify error with default client")
	} else if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("expected certificate error, got: %v", err)
	}

	insecureGuard, err := New(Config{
		AllowPrivateNetwork:   true,
		TLSInsecureSkipVerify: true,
		LookupHost: func(ctx context.Context, host string) ([]string, error) {
			return []string{"127.0.0.1"}, nil
		},
	})
	if err != nil {
		t.Fatalf("New insecure: %v", err)
	}
	insecureClient := insecureGuard.HTTPClient(2 * time.Second)
	transport, ok := insecureClient.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("expected transport TLSClientConfig.InsecureSkipVerify=true")
	}

	req2, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := insecureClient.Do(req2)
	if err != nil {
		t.Fatalf("insecure fetch: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("body=%q", body)
	}
	if resp.TLS == nil {
		t.Fatal("expected TLS connection info")
	}
}

func TestHTTPClient_ReusesCachedClient(t *testing.T) {
	guard, err := New(Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	a := guard.HTTPClient(2 * time.Second)
	b := guard.HTTPClient(2 * time.Second)
	if a != b {
		t.Fatal("expected cached http.Client")
	}
	c := guard.HTTPClientForFetch(2*time.Second, true)
	if c == a {
		t.Fatal("insecure client should be a different cache entry")
	}
	d := guard.HTTPClientForFetch(2*time.Second, true)
	if c != d {
		t.Fatal("expected cached insecure client")
	}
}
