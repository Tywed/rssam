package telegram

import (
	"fmt"
	"strings"
	"time"

	"rssam/internal/proxy"
)

// Config is service-level Telegram bridge configuration (from env).
type Config struct {
	ProxyServiceURL     string
	ProxyServiceToken   string
	ProxyTargetURL      string
	StaticProxy         string
	UseProxy            bool
	ProxyConnectTimeout time.Duration
	ProxyRequestTimeout time.Duration
	ProxyRetry          int
	MaxPages            int
	UserAgent           string
	TLSInsecureSkipVerify bool
}

// ProxyMode selects how Telegram channel pages are fetched.
type ProxyMode string

const (
	ProxyModeService ProxyMode = "service"
	ProxyModeStatic  ProxyMode = "static"
	ProxyModeDirect  ProxyMode = "direct"
)

// EffectiveConfig merges service defaults with optional per-feed bridge_state overrides.
func EffectiveConfig(base Config, override *BridgeOverride) Config {
	out := base
	if override == nil {
		return out
	}
	if override.UseProxy != nil {
		out.UseProxy = *override.UseProxy
	}
	if s := strings.TrimSpace(override.ProxyServiceURL); s != "" {
		out.ProxyServiceURL = s
	}
	if s := strings.TrimSpace(override.ProxyTargetURL); s != "" {
		out.ProxyTargetURL = s
	}
	if s := strings.TrimSpace(override.StaticProxy); s != "" {
		out.StaticProxy = s
	}
	if override.MaxPages > 0 {
		out.MaxPages = override.MaxPages
	}
	return out
}

// ResolveProxyMode picks fetch strategy for a merged config.
func (c Config) ResolveProxyMode() ProxyMode {
	if !c.UseProxy {
		return ProxyModeDirect
	}
	if strings.TrimSpace(c.StaticProxy) != "" {
		return ProxyModeStatic
	}
	return ProxyModeService
}

// StaticProxyEndpoint returns parsed static proxy or error.
func (c Config) StaticProxyEndpoint() (proxy.Proxy, error) {
	return proxy.ParseStaticProxy(c.StaticProxy)
}

// BridgeOverride is optional per-feed JSON in bridge_state.telegram.
type BridgeOverride struct {
	UseProxy        *bool  `json:"use_proxy,omitempty"`
	ProxyServiceURL string `json:"proxy_service_url,omitempty"`
	ProxyTargetURL  string `json:"proxy_target_url,omitempty"`
	StaticProxy     string `json:"static_proxy,omitempty"`
	MaxPages        int    `json:"max_pages,omitempty"`
}

func (c Config) validateFetch() error {
	switch c.ResolveProxyMode() {
	case ProxyModeDirect:
		return nil
	case ProxyModeStatic:
		if _, err := c.StaticProxyEndpoint(); err != nil {
			return fmt.Errorf("telegram: static proxy: %w", err)
		}
		return nil
	default:
		if strings.TrimSpace(c.ProxyServiceURL) == "" {
			return fmt.Errorf("telegram: TELEGRAM_PROXY_SERVICE_URL is required (or set TELEGRAM_STATIC_PROXY / disable proxy per feed)")
		}
		return nil
	}
}
