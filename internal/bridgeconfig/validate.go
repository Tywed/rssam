package bridgeconfig

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"rssam/internal/proxy"
)

// Validate rejects settings that a bridge would silently fall back from
// (unparsable duration, malformed proxy or URL): the form reports the
// error and app_settings keeps the previous row.
func (s Stored) Validate() error {
	for name, raw := range map[string]string{
		"telegram connect_timeout": s.Telegram.ConnectTimeout,
		"telegram request_timeout": s.Telegram.RequestTimeout,
		"max default_lookback":     s.Max.DefaultLookback,
		"max overlap":              s.Max.Overlap,
		"vk default_lookback":      s.VK.DefaultLookback,
		"vk overlap":               s.VK.Overlap,
	} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		d, err := time.ParseDuration(strings.TrimSpace(raw))
		if err != nil || d < 0 {
			return fmt.Errorf("%s: invalid duration %q", name, raw)
		}
	}
	for name, raw := range map[string]string{
		"telegram proxy_service_url": s.Telegram.ProxyServiceURL,
		"telegram proxy_target_url":  s.Telegram.ProxyTargetURL,
		"max api_base_url":           s.Max.APIBaseURL,
		"rutube api_base_url":        s.Rutube.APIBaseURL,
	} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%s: must be an absolute http(s) URL", name)
		}
	}
	if strings.TrimSpace(s.Telegram.StaticProxy) != "" {
		if _, err := proxy.ParseStaticProxy(s.Telegram.StaticProxy); err != nil {
			return fmt.Errorf("telegram static_proxy: %w", err)
		}
	}
	for name, n := range map[string]int{
		"telegram proxy_retry":    s.Telegram.ProxyRetry,
		"telegram max_pages":      s.Telegram.MaxPages,
		"max default_limit":       s.Max.DefaultLimit,
		"max rate_limit_seconds":  s.Max.RateLimitSeconds,
		"max request_interval_ms": s.Max.RequestIntervalMs,
		"vk default_count":        s.VK.DefaultCount,
		"vk rate_limit_seconds":   s.VK.RateLimitSeconds,
	} {
		if n < 0 {
			return fmt.Errorf("%s: must not be negative", name)
		}
	}
	return nil
}
