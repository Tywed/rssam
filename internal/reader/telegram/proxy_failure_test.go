package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/proxy"
	"rssam/internal/ssrf"
)

// A proxy-service outage and a dead SOCKS endpoint both surface as
// proxy.Unavailable and count against Health; the channel page itself is
// never reached, so nothing about the feed is concluded.
func TestFetch_ProxyFailuresAreUnavailable(t *testing.T) {
	// A listener that is closed immediately: the port refuses connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	serviceUp := true
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/proxy/bad" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !serviceUp {
			http.Error(w, "no proxies", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "p1", "host": "127.0.0.1", "port": deadPort})
	}))
	defer svc.Close()

	pc := proxy.NewClient(svc.Client())
	h := NewHandler(pc, nil, Config{
		UseProxy:            true,
		ProxyServiceURL:     svc.URL,
		ProxyRetry:          1,
		ProxyConnectTimeout: time.Second,
		ProxyRequestTimeout: 2 * time.Second,
	})
	ctx := context.Background()

	_, err = h.Fetch(ctx, bridge.Request{FeedURL: "https://t.me/chan"})
	var u *proxy.Unavailable
	if !errors.As(err, &u) || u.Op != "socks fetch" {
		t.Fatalf("dead SOCKS endpoint: want Unavailable{socks fetch}, got %v", err)
	}

	serviceUp = false
	_, err = h.Fetch(ctx, bridge.Request{FeedURL: "https://t.me/chan"})
	if !errors.As(err, &u) || u.Op != "get proxy" {
		t.Fatalf("service down: want Unavailable{get proxy}, got %v", err)
	}

	// Two failed polls, no success in between: the streak is 2 and only
	// the duration keeps it from being an outage.
	for range 30 {
		_, _ = h.Fetch(ctx, bridge.Request{FeedURL: "https://t.me/chan"})
	}
	if _, down := pc.Health.Outage(); down {
		t.Fatal("a fresh streak must not alert yet")
	}
	if errors.Unwrap(u) == nil {
		t.Fatal("Unavailable must keep the cause")
	}
}

// Static-proxy mode wraps the same way; direct mode does not involve the
// proxy at all and keeps plain errors.
func TestFetch_StaticProxyFailureIsUnavailable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	h := NewHandler(proxy.NewClient(nil), nil, Config{
		UseProxy:            true,
		StaticProxy:         "socks5://" + addr,
		ProxyConnectTimeout: time.Second,
		ProxyRequestTimeout: 2 * time.Second,
	})
	_, err = h.Fetch(context.Background(), bridge.Request{FeedURL: "https://t.me/chan"})
	if !proxy.IsUnavailable(err) {
		t.Fatalf("static proxy: want Unavailable, got %v", err)
	}
}

// A static proxy set on the feed, unlike the operator's, is subject to the
// fetch policy: the poller must not be pointed at an address on the LAN.
func TestFetch_PerFeedStaticProxyIsGuarded(t *testing.T) {
	guard, err := ssrf.New(ssrf.Config{})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(proxy.NewClient(nil), nil, Config{
		UseProxy:            true,
		StaticProxy:         "socks5://127.0.0.1:9", // operator's own proxy: trusted as is
		ProxyConnectTimeout: time.Second,
		ProxyRequestTimeout: 2 * time.Second,
	})
	h.SetSSRFGuard(guard)
	_, err = h.Fetch(context.Background(), bridge.Request{FeedURL: "https://t.me/chan"})
	if !proxy.IsUnavailable(err) {
		t.Fatalf("operator proxy: want Unavailable (connection refused), got %v", err)
	}

	state := bridge.EncodeState(BridgeOverride{StaticProxy: "socks5://192.168.1.10:1080"})
	_, err = h.Fetch(context.Background(), bridge.Request{FeedURL: "https://t.me/chan", State: state})
	if err == nil || proxy.IsUnavailable(err) || !strings.Contains(err.Error(), "ssrf") {
		t.Fatalf("per-feed private proxy: want ssrf refusal, got %v", err)
	}
}
