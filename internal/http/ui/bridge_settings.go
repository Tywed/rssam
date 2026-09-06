package ui

import (
	"strings"
	"time"

	"rssam/internal/bridgeconfig"
	"rssam/internal/config"
)

// BridgeSettings holds runtime bridge configuration shown in the settings UI.
type BridgeSettings struct {
	Telegram TelegramBridgeSettings
	Max      MaxBridgeSettings
	VK       VKBridgeSettings
	Rutube   RutubeBridgeSettings
}

type TelegramBridgeSettings struct {
	Enabled              bool
	UseProxy             bool
	ProxyServiceURL      string
	ProxyServiceTokenSet bool
	ProxyTargetURL       string
	StaticProxy          string
	ConnectTimeout       time.Duration
	RequestTimeout       time.Duration
	ProxyRetry           int
	MaxPages             int
}

type MaxBridgeSettings struct {
	Enabled           bool
	APIBaseURL        string
	DefaultLimit      int
	DefaultLookback   time.Duration
	Overlap           time.Duration
	RateLimitSeconds  int
	RequestIntervalMs int
	ConcurrentSlots   int
	AllowPrivateAPI   bool
}

type VKBridgeSettings struct {
	Enabled          bool
	AccessTokenSet   bool
	APIVersion       string
	DefaultCount     int
	DefaultLookback  time.Duration
	Overlap          time.Duration
	RateLimitSeconds int
}

type RutubeBridgeSettings struct {
	Enabled    bool
	APIBaseURL string
}

func BridgeSettingsFromRuntime(rt bridgeconfig.Runtime) BridgeSettings {
	slots := max(rt.Max.ConcurrentSlots, 1)
	return BridgeSettings{
		Telegram: TelegramBridgeSettings{
			Enabled:              true,
			UseProxy:             rt.Telegram.UseProxy,
			ProxyServiceURL:      rt.Telegram.ProxyServiceURL,
			ProxyServiceTokenSet: rt.Telegram.ProxyServiceToken != "",
			ProxyTargetURL:       rt.Telegram.ProxyTargetURL,
			StaticProxy:          rt.Telegram.StaticProxy,
			ConnectTimeout:       rt.Telegram.ConnectTimeout,
			RequestTimeout:       rt.Telegram.RequestTimeout,
			ProxyRetry:           rt.Telegram.ProxyRetry,
			MaxPages:             rt.Telegram.MaxPages,
		},
		Max: MaxBridgeSettings{
			Enabled:           rt.Max.APIBaseURL != "",
			APIBaseURL:        rt.Max.APIBaseURL,
			DefaultLimit:      rt.Max.DefaultLimit,
			DefaultLookback:   rt.Max.DefaultLookback,
			Overlap:           rt.Max.Overlap,
			RateLimitSeconds:  rt.Max.RateLimitSeconds,
			RequestIntervalMs: rt.Max.RequestIntervalMs,
			ConcurrentSlots:   slots,
			AllowPrivateAPI:   rt.Max.AllowPrivateAPI,
		},
		VK: VKBridgeSettings{
			Enabled:          rt.VK.AccessToken != "",
			AccessTokenSet:   rt.VK.AccessToken != "",
			APIVersion:       rt.VK.APIVersion,
			DefaultCount:     rt.VK.DefaultCount,
			DefaultLookback:  rt.VK.DefaultLookback,
			Overlap:          rt.VK.Overlap,
			RateLimitSeconds: rt.VK.RateLimitSeconds,
		},
		Rutube: RutubeBridgeSettings{
			Enabled:    rt.Rutube.APIBaseURL != "",
			APIBaseURL: rt.Rutube.APIBaseURL,
		},
	}
}

func BridgeSettingsFrom(cfg config.Config) BridgeSettings {
	return BridgeSettingsFromRuntime(bridgeconfig.MergeEnv(cfg, bridgeconfig.Stored{}))
}

func (s TelegramBridgeSettings) ProxyConfigured() bool {
	return strings.TrimSpace(s.ProxyServiceURL) != "" || strings.TrimSpace(s.StaticProxy) != ""
}
