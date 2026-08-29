package bridgeconfig

import (
	"encoding/json"
	"strings"
	"time"

	"rssam/internal/config"
)

const SettingsKey = "bridges"

// Stored is persisted in app_settings (JSON).
type Stored struct {
	Telegram TelegramStored `json:"telegram"`
	Max      MaxStored      `json:"max"`
	VK       VKStored       `json:"vk"`
	Rutube   RutubeStored   `json:"rutube"`
}

type TelegramStored struct {
	UseProxy            bool   `json:"use_proxy"`
	ProxyServiceURL     string `json:"proxy_service_url"`
	ProxyServiceToken   string `json:"proxy_service_token"`
	ProxyTargetURL      string `json:"proxy_target_url"`
	StaticProxy         string `json:"static_proxy"`
	ConnectTimeout      string `json:"connect_timeout"`
	RequestTimeout      string `json:"request_timeout"`
	ProxyRetry          int    `json:"proxy_retry"`
	MaxPages            int    `json:"max_pages"`
}

type MaxStored struct {
	APIBaseURL       string `json:"api_base_url"`
	DefaultLimit     int    `json:"default_limit"`
	DefaultLookback  string `json:"default_lookback"`
	Overlap          string `json:"overlap"`
	RateLimitSeconds int    `json:"rate_limit_seconds"`
	AllowPrivateAPI  bool   `json:"allow_private_api"`
}

type VKStored struct {
	AccessToken      string `json:"access_token"`
	APIVersion       string `json:"api_version"`
	DefaultCount     int    `json:"default_count"`
	DefaultLookback  string `json:"default_lookback"`
	Overlap          string `json:"overlap"`
	RateLimitSeconds int    `json:"rate_limit_seconds"`
}

type RutubeStored struct {
	APIBaseURL string `json:"api_base_url"`
}

// Runtime holds merged env + DB settings used by handlers and UI.
type Runtime struct {
	Telegram TelegramRuntime
	Max      MaxRuntime
	VK       VKRuntime
	Rutube   RutubeRuntime
}

type TelegramRuntime struct {
	UseProxy            bool
	ProxyServiceURL     string
	ProxyServiceToken   string
	ProxyTargetURL      string
	StaticProxy         string
	ConnectTimeout      time.Duration
	RequestTimeout      time.Duration
	ProxyRetry          int
	MaxPages            int
}

type MaxRuntime struct {
	APIBaseURL        string
	DefaultLimit      int
	DefaultLookback   time.Duration
	Overlap           time.Duration
	RateLimitSeconds  int
	AllowPrivateAPI   bool
}

type VKRuntime struct {
	AccessToken      string
	APIVersion       string
	DefaultCount     int
	DefaultLookback  time.Duration
	Overlap          time.Duration
	RateLimitSeconds int
}

type RutubeRuntime struct {
	APIBaseURL string
}

func MergeEnv(cfg config.Config, stored Stored) Runtime {
	rt := Runtime{
		Telegram: TelegramRuntime{
			UseProxy:            cfg.TelegramProxyServiceURL != "" || cfg.TelegramStaticProxy != "",
			ProxyServiceURL:     cfg.TelegramProxyServiceURL,
			ProxyServiceToken:   cfg.TelegramProxyServiceToken,
			ProxyTargetURL:      cfg.TelegramProxyTargetURL,
			StaticProxy:         cfg.TelegramStaticProxy,
			ConnectTimeout:      cfg.TelegramProxyConnectTimeout,
			RequestTimeout:      cfg.TelegramProxyRequestTimeout,
			ProxyRetry:          cfg.TelegramProxyRetry,
			MaxPages:            cfg.TelegramMaxPages,
		},
		Max: MaxRuntime{
			APIBaseURL:       cfg.MaxAPIBaseURL,
			DefaultLimit:     cfg.MaxDefaultLimit,
			DefaultLookback:  cfg.MaxDefaultLookback,
			Overlap:          cfg.MaxOverlap,
			RateLimitSeconds: cfg.MaxRateLimitSeconds,
			AllowPrivateAPI:  cfg.MaxAllowPrivateAPI,
		},
		VK: VKRuntime{
			AccessToken:      cfg.VKAccessToken,
			APIVersion:       cfg.VKAPIVersion,
			DefaultCount:     cfg.VKDefaultCount,
			DefaultLookback:  cfg.VKDefaultLookback,
			Overlap:          cfg.VKOverlap,
			RateLimitSeconds: cfg.VKRateLimitSeconds,
		},
		Rutube: RutubeRuntime{
			APIBaseURL: cfg.RutubeAPIBaseURL,
		},
	}
	applyTelegramStored(&rt.Telegram, stored.Telegram)
	applyMaxStored(&rt.Max, stored.Max)
	applyVKStored(&rt.VK, stored.VK)
	applyRutubeStored(&rt.Rutube, stored.Rutube)
	return rt
}

func applyTelegramStored(dst *TelegramRuntime, s TelegramStored) {
	if s.UseProxy || strings.TrimSpace(s.ProxyServiceURL) != "" || strings.TrimSpace(s.StaticProxy) != "" {
		dst.UseProxy = s.UseProxy || strings.TrimSpace(s.ProxyServiceURL) != "" || strings.TrimSpace(s.StaticProxy) != ""
	}
	if v := strings.TrimSpace(s.ProxyServiceURL); v != "" {
		dst.ProxyServiceURL = v
	}
	if v := strings.TrimSpace(s.ProxyServiceToken); v != "" {
		dst.ProxyServiceToken = v
	}
	if v := strings.TrimSpace(s.ProxyTargetURL); v != "" {
		dst.ProxyTargetURL = v
	}
	if v := strings.TrimSpace(s.StaticProxy); v != "" {
		dst.StaticProxy = v
	}
	if d, ok := parseDuration(s.ConnectTimeout); ok {
		dst.ConnectTimeout = d
	}
	if d, ok := parseDuration(s.RequestTimeout); ok {
		dst.RequestTimeout = d
	}
	if s.ProxyRetry >= 0 {
		dst.ProxyRetry = s.ProxyRetry
	}
	if s.MaxPages > 0 {
		dst.MaxPages = s.MaxPages
	}
}

func applyMaxStored(dst *MaxRuntime, s MaxStored) {
	if v := strings.TrimSpace(s.APIBaseURL); v != "" {
		dst.APIBaseURL = v
	}
	if s.DefaultLimit > 0 {
		dst.DefaultLimit = s.DefaultLimit
	}
	if d, ok := parseDuration(s.DefaultLookback); ok {
		dst.DefaultLookback = d
	}
	if d, ok := parseDuration(s.Overlap); ok {
		dst.Overlap = d
	}
	if s.RateLimitSeconds > 0 {
		dst.RateLimitSeconds = s.RateLimitSeconds
	}
	dst.AllowPrivateAPI = s.AllowPrivateAPI
}

func applyVKStored(dst *VKRuntime, s VKStored) {
	if v := strings.TrimSpace(s.AccessToken); v != "" {
		dst.AccessToken = v
	}
	if v := strings.TrimSpace(s.APIVersion); v != "" {
		dst.APIVersion = v
	}
	if s.DefaultCount > 0 {
		dst.DefaultCount = s.DefaultCount
	}
	if d, ok := parseDuration(s.DefaultLookback); ok {
		dst.DefaultLookback = d
	}
	if d, ok := parseDuration(s.Overlap); ok {
		dst.Overlap = d
	}
	if s.RateLimitSeconds > 0 {
		dst.RateLimitSeconds = s.RateLimitSeconds
	}
}

func applyRutubeStored(dst *RutubeRuntime, s RutubeStored) {
	if v := strings.TrimSpace(s.APIBaseURL); v != "" {
		dst.APIBaseURL = v
	}
}

func RuntimeToStored(rt Runtime) Stored {
	return Stored{
		Telegram: TelegramStored{
			UseProxy:            rt.Telegram.UseProxy,
			ProxyServiceURL:     rt.Telegram.ProxyServiceURL,
			ProxyServiceToken:   rt.Telegram.ProxyServiceToken,
			ProxyTargetURL:      rt.Telegram.ProxyTargetURL,
			StaticProxy:         rt.Telegram.StaticProxy,
			ConnectTimeout:      rt.Telegram.ConnectTimeout.String(),
			RequestTimeout:      rt.Telegram.RequestTimeout.String(),
			ProxyRetry:          rt.Telegram.ProxyRetry,
			MaxPages:            rt.Telegram.MaxPages,
		},
		Max: MaxStored{
			APIBaseURL:       rt.Max.APIBaseURL,
			DefaultLimit:     rt.Max.DefaultLimit,
			DefaultLookback:  rt.Max.DefaultLookback.String(),
			Overlap:          rt.Max.Overlap.String(),
			RateLimitSeconds: rt.Max.RateLimitSeconds,
			AllowPrivateAPI:  rt.Max.AllowPrivateAPI,
		},
		VK: VKStored{
			AccessToken:      rt.VK.AccessToken,
			APIVersion:       rt.VK.APIVersion,
			DefaultCount:     rt.VK.DefaultCount,
			DefaultLookback:  rt.VK.DefaultLookback.String(),
			Overlap:          rt.VK.Overlap.String(),
			RateLimitSeconds: rt.VK.RateLimitSeconds,
		},
		Rutube: RutubeStored{
			APIBaseURL: rt.Rutube.APIBaseURL,
		},
	}
}

func ParseStored(raw json.RawMessage) Stored {
	if len(raw) == 0 {
		return Stored{}
	}
	var s Stored
	_ = json.Unmarshal(raw, &s)
	return s
}

func parseDuration(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	return d, true
}

func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}
