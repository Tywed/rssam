package reader

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"rssam/internal/proxy"
	"rssam/internal/reader/max"
	"rssam/internal/reader/maxstat"
	"rssam/internal/reader/dzen"
	"rssam/internal/reader/rutube"
	"rssam/internal/reader/smotrim"
	"rssam/internal/reader/telegram"
	"rssam/internal/reader/vk"
	"rssam/internal/ssrf"
)

// RegistryConfig builds a HandlerRegistry for the feed refresher.
type RegistryConfig struct {
	HTTPClient           *http.Client
	SSRFGuard            *ssrf.Guard
	UserAgent            string
	FetchAllowPrivateNet bool
	FetchViaProxyURL     string
	FetchTLSInsecure     bool

	MaxAPIBaseURL       string
	MaxDefaultLimit     int
	MaxDefaultLookback  time.Duration
	MaxOverlap          time.Duration
	MaxRateLimitSeconds int
	MaxAllowPrivateAPI  bool

	MaxstatAccessToken      string
	MaxstatAPIBaseURL       string
	MaxstatDefaultLimit     int
	MaxstatDefaultLookback  time.Duration
	MaxstatOverlap          time.Duration
	MaxstatRateLimitSeconds int

	TelegramProxyServiceURL     string
	TelegramProxyServiceToken   string
	TelegramProxyTargetURL      string
	TelegramStaticProxy         string
	TelegramProxyConnectTimeout time.Duration
	TelegramProxyRequestTimeout time.Duration
	TelegramProxyRetry          int
	TelegramMaxPages            int

	VKAccessToken      string
	VKAPIVersion       string
	VKDefaultCount     int
	VKDefaultLookback  time.Duration
	VKOverlap          time.Duration
	VKRateLimitSeconds int

	RutubeAPIBaseURL string

	DzenSearchURL string
	DzenUserAgent string
	DzenCookie    string

	SmotrimBrandBaseURL string
	SmotrimGraphQLURL   string
	SmotrimUserAgent    string
}

// RegistryBundle contains the handler registry and direct handler refs for config reload.
type RegistryBundle struct {
	Registry *HandlerRegistry
	Telegram *telegram.Handler
	Max      *max.Handler
	Maxstat  *maxstat.Handler
	VK       *vk.Handler
	Rutube   *rutube.Handler
	Dzen     *dzen.Handler
	Smotrim  *smotrim.Handler
}

// NewRegistry creates a registry with RSS (fallback) and optional bridge handlers.
func NewRegistry(cfg RegistryConfig) (*RegistryBundle, error) {
	rssFetcher := NewRSSFetcher(cfg.HTTPClient, cfg.UserAgent, cfg.SSRFGuard, cfg.FetchViaProxyURL)
	reg := NewHandlerRegistry()

	proxyHTTP := proxyServiceHTTPClient(cfg)
	proxyClient := proxy.NewClient(proxyHTTP)
	if tok := strings.TrimSpace(cfg.TelegramProxyServiceToken); tok != "" {
		proxyClient.ServiceToken = tok
	}
	tgHandler := telegram.NewHandler(proxyClient, cfg.HTTPClient, telegram.Config{
		ProxyServiceURL:         cfg.TelegramProxyServiceURL,
		ProxyServiceToken:       cfg.TelegramProxyServiceToken,
		ProxyTargetURL:          cfg.TelegramProxyTargetURL,
		StaticProxy:             cfg.TelegramStaticProxy,
		UseProxy:                cfg.TelegramProxyServiceURL != "" || cfg.TelegramStaticProxy != "",
		ProxyConnectTimeout:     cfg.TelegramProxyConnectTimeout,
		ProxyRequestTimeout:     cfg.TelegramProxyRequestTimeout,
		ProxyRetry:              cfg.TelegramProxyRetry,
		MaxPages:                cfg.TelegramMaxPages,
		UserAgent:               cfg.UserAgent,
		TLSInsecureSkipVerify:   cfg.FetchTLSInsecure,
	})
	reg.Register(newTelegramHandler(tgHandler))

	var maxHandler *max.Handler
	if cfg.MaxAPIBaseURL != "" {
		h, err := max.NewHandler(cfg.HTTPClient, cfg.SSRFGuard, max.Config{
			APIBaseURL:        cfg.MaxAPIBaseURL,
			DefaultLimit:      cfg.MaxDefaultLimit,
			DefaultLookback:   cfg.MaxDefaultLookback,
			Overlap:           cfg.MaxOverlap,
			RateLimitCooldown: time.Duration(cfg.MaxRateLimitSeconds) * time.Second,
			AllowPrivateAPI:   cfg.MaxAllowPrivateAPI,
			FetchAllowPrivate: cfg.FetchAllowPrivateNet,
		})
		if err != nil {
			return nil, fmt.Errorf("max handler: %w", err)
		}
		maxHandler = h
		reg.Register(newMaxHandler(maxHandler))
	}

	maxstatClient, err := maxstat.NewClient(cfg.HTTPClient, cfg.SSRFGuard, maxstat.Config{
		AccessToken:       cfg.MaxstatAccessToken,
		APIBaseURL:        cfg.MaxstatAPIBaseURL,
		DefaultLimit:      cfg.MaxstatDefaultLimit,
		DefaultLookback:   cfg.MaxstatDefaultLookback,
		Overlap:           cfg.MaxstatOverlap,
		RateLimitCooldown: time.Duration(cfg.MaxstatRateLimitSeconds) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("maxstat client: %w", err)
	}
	maxstatHandler := maxstat.NewHandler(maxstatClient, maxstat.Config{
		AccessToken:       cfg.MaxstatAccessToken,
		APIBaseURL:        cfg.MaxstatAPIBaseURL,
		DefaultLimit:      cfg.MaxstatDefaultLimit,
		DefaultLookback:   cfg.MaxstatDefaultLookback,
		Overlap:           cfg.MaxstatOverlap,
		RateLimitCooldown: time.Duration(cfg.MaxstatRateLimitSeconds) * time.Second,
	})
	reg.Register(newMaxstatHandler(maxstatHandler))

	var vkHandler *vk.Handler
	if strings.TrimSpace(cfg.VKAccessToken) != "" {
		vkClient, err := vk.NewClient(cfg.HTTPClient, cfg.SSRFGuard, vk.Config{
			AccessToken:       cfg.VKAccessToken,
			APIVersion:        cfg.VKAPIVersion,
			DefaultCount:      cfg.VKDefaultCount,
			DefaultLookback:   cfg.VKDefaultLookback,
			Overlap:           cfg.VKOverlap,
			RateLimitCooldown: time.Duration(cfg.VKRateLimitSeconds) * time.Second,
		})
		if err != nil {
			return nil, fmt.Errorf("vk client: %w", err)
		}
		vkHandler = vk.NewHandler(vkClient, vk.Config{
			AccessToken:       cfg.VKAccessToken,
			APIVersion:        cfg.VKAPIVersion,
			DefaultCount:      cfg.VKDefaultCount,
			DefaultLookback:   cfg.VKDefaultLookback,
			Overlap:           cfg.VKOverlap,
			RateLimitCooldown: time.Duration(cfg.VKRateLimitSeconds) * time.Second,
		})
		reg.Register(newVKSearchHandler(vkHandler))
	}

	rutubeClient, err := rutube.NewClient(cfg.HTTPClient, cfg.SSRFGuard, rutube.Config{
		APIBaseURL: cfg.RutubeAPIBaseURL,
		UserAgent:  cfg.UserAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("rutube client: %w", err)
	}
	rutubeHandler := rutube.NewHandler(rutubeClient, rutube.Config{
		APIBaseURL: cfg.RutubeAPIBaseURL,
		UserAgent:  cfg.UserAgent,
	})
	reg.Register(newRutubeHandler(rutubeHandler))

	dzenClient, err := dzen.NewClient(cfg.HTTPClient, cfg.SSRFGuard, dzen.Config{
		SearchURL: cfg.DzenSearchURL,
		UserAgent: cfg.DzenUserAgent,
		Cookie:    cfg.DzenCookie,
	})
	if err != nil {
		return nil, fmt.Errorf("dzen client: %w", err)
	}
	dzenHandler := dzen.NewHandler(dzenClient, dzen.Config{
		SearchURL: cfg.DzenSearchURL,
		UserAgent: cfg.DzenUserAgent,
		Cookie:    cfg.DzenCookie,
	})
	reg.Register(newDzenNewsHandler(dzenHandler))

	smotrimClient, err := smotrim.NewClient(cfg.HTTPClient, cfg.SSRFGuard, smotrim.Config{
		BrandBaseURL: cfg.SmotrimBrandBaseURL,
		GraphQLURL:   cfg.SmotrimGraphQLURL,
		UserAgent:    cfg.SmotrimUserAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("smotrim client: %w", err)
	}
	smotrimHandler := smotrim.NewHandler(smotrimClient, smotrim.Config{
		BrandBaseURL: cfg.SmotrimBrandBaseURL,
		GraphQLURL:   cfg.SmotrimGraphQLURL,
		UserAgent:    cfg.SmotrimUserAgent,
	})
	reg.Register(newSmotrimHandler(smotrimHandler))

	reg.Register(NewRSSHandler(rssFetcher))
	return &RegistryBundle{
		Registry: reg,
		Telegram: tgHandler,
		Max:      maxHandler,
		Maxstat:  maxstatHandler,
		VK:       vkHandler,
		Rutube:   rutubeHandler,
		Dzen:     dzenHandler,
		Smotrim:  smotrimHandler,
	}, nil
}

func proxyServiceHTTPClient(cfg RegistryConfig) *http.Client {
	timeout := 15 * time.Second
	if cfg.TelegramProxyConnectTimeout > 0 {
		timeout += cfg.TelegramProxyConnectTimeout
	}
	if cfg.TelegramProxyRequestTimeout > 0 {
		timeout += cfg.TelegramProxyRequestTimeout
	}
	if cfg.SSRFGuard != nil {
		return cfg.SSRFGuard.HTTPClient(timeout)
	}
	return &http.Client{Timeout: timeout}
}
