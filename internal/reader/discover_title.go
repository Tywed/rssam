package reader

import (
	"context"
	"fmt"
	"strings"

	"rssam/internal/proxy"
	"rssam/internal/reader/telegram"
)

// TitleResolver discovers feed titles when the user leaves the title empty (TT-RSS-style).
type TitleResolver struct {
	rss      *RSSFetcher
	telegram *telegram.Handler
}

// NewTitleResolver builds a title resolver. If tg is non-nil, Telegram title
// discovery reuses the poller's proxy client instead of allocating a second one.
func NewTitleResolver(cfg RegistryConfig, tg *telegram.Handler) (*TitleResolver, error) {
	rss := NewRSSFetcher(cfg.HTTPClient, cfg.UserAgent, cfg.SSRFGuard, cfg.FetchViaProxyURL)
	if tg != nil {
		return &TitleResolver{rss: rss, telegram: tg}, nil
	}
	proxyHTTP := proxyServiceHTTPClient(cfg)
	proxyClient := proxy.NewClient(proxyHTTP)
	if tok := strings.TrimSpace(cfg.TelegramProxyServiceToken); tok != "" {
		proxyClient.ServiceToken = tok
	}
	created := telegram.NewHandler(proxyClient, cfg.HTTPClient, telegram.Config{
		ProxyServiceURL:     cfg.TelegramProxyServiceURL,
		ProxyServiceToken:   cfg.TelegramProxyServiceToken,
		ProxyTargetURL:      cfg.TelegramProxyTargetURL,
		StaticProxy:         cfg.TelegramStaticProxy,
		UseProxy:            cfg.TelegramProxyServiceURL != "" || cfg.TelegramStaticProxy != "",
		ProxyConnectTimeout: cfg.TelegramProxyConnectTimeout,
		ProxyRequestTimeout: cfg.TelegramProxyRequestTimeout,
		ProxyRetry:          cfg.TelegramProxyRetry,
		MaxPages:            1,
		UserAgent:           cfg.UserAgent,
	})
	return &TitleResolver{rss: rss, telegram: created}, nil
}

// DiscoverTitle returns a human-readable feed title for feedURL.
func (r *TitleResolver) DiscoverTitle(ctx context.Context, feedURL, feedType string, tlsInsecure bool) (string, error) {
	if r == nil {
		return "", fmt.Errorf("title resolver not configured")
	}
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return "", fmt.Errorf("empty feed url")
	}
	ft := NormalizeFeedType(feedType)
	if ft == "" {
		ft = DetectFeedTypeFromURL(feedURL)
	}
	switch ft {
	case FeedTypeTelegram:
		if r.telegram == nil {
			return "", fmt.Errorf("telegram handler not configured")
		}
		return r.telegram.DiscoverChannelTitle(ctx, feedURL)
	default:
		if r.rss == nil {
			return "", fmt.Errorf("rss fetcher not configured")
		}
		return r.rss.DiscoverTitle(ctx, feedURL, false, tlsInsecure)
	}
}
