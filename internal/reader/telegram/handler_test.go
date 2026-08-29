package telegram

import (
	"context"
	"testing"
	"time"

	"rssam/internal/proxy"
)

func TestHandler_Fetch_requiresProxyService(t *testing.T) {
	h := NewHandler(proxy.NewClient(nil), nil, Config{UseProxy: true})
	_, err := h.Fetch(context.Background(), "https://t.me/s/demo", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHandler_Fetch_invalidURL(t *testing.T) {
	h := NewHandler(proxy.NewClient(nil), nil, Config{ProxyServiceURL: "http://127.0.0.1:9", UseProxy: true})
	_, err := h.Fetch(context.Background(), "https://example.com/not-telegram", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEffectiveConfig_override(t *testing.T) {
	use := false
	base := Config{ProxyServiceURL: "http://a", MaxPages: 1, UseProxy: true}
	out := EffectiveConfig(base, &BridgeOverride{ProxyServiceURL: "http://b", MaxPages: 3, UseProxy: &use, StaticProxy: "127.0.0.1:1080"})
	if out.ProxyServiceURL != "http://b" || out.MaxPages != 3 || out.UseProxy || out.StaticProxy != "127.0.0.1:1080" {
		t.Fatalf("%+v", out)
	}
}

func TestHandler_defaults(t *testing.T) {
	h := NewHandler(nil, nil, Config{ProxyRetry: -1})
	if h.cfg.ProxyRetry != 0 {
		t.Fatalf("retry=%d", h.cfg.ProxyRetry)
	}
	if h.cfg.ProxyConnectTimeout != 10*time.Second {
		t.Fatalf("connect=%s", h.cfg.ProxyConnectTimeout)
	}
}

func TestResolveProxyMode(t *testing.T) {
	if (Config{UseProxy: false}).ResolveProxyMode() != ProxyModeDirect {
		t.Fatal("want direct")
	}
	if (Config{UseProxy: true, StaticProxy: "127.0.0.1:1080"}).ResolveProxyMode() != ProxyModeStatic {
		t.Fatal("want static")
	}
	if (Config{UseProxy: true, ProxyServiceURL: "http://x"}).ResolveProxyMode() != ProxyModeService {
		t.Fatal("want service")
	}
}
