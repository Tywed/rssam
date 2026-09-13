package reader

import (
	"context"
	"fmt"
	"strings"

	"rssam/internal/proxy"
	"rssam/internal/reader/page"
	"rssam/internal/reader/telegram"
)

// TitleResolver discovers feed titles when the user leaves the title empty (TT-RSS-style).
type TitleResolver struct {
	rss      *RSSFetcher
	telegram *telegram.Handler
	page     *page.Handler
}

// NewTitleResolver builds a title resolver. If tg is non-nil, Telegram title
// discovery reuses the poller's proxy client instead of allocating a second one.
func NewTitleResolver(cfg RegistryConfig, tg *telegram.Handler) (*TitleResolver, error) {
	rss := NewRSSFetcher(cfg.HTTPClient, cfg.UserAgent, cfg.SSRFGuard, cfg.FetchViaProxyURL)
	pg := page.NewHandler(cfg.HTTPClient, cfg.SSRFGuard, cfg.UserAgent)
	if tg != nil {
		return &TitleResolver{rss: rss, telegram: tg, page: pg}, nil
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
	return &TitleResolver{rss: rss, telegram: created, page: pg}, nil
}

// DiscoverTitle returns a human-readable feed title for feedURL.
func (r *TitleResolver) DiscoverTitle(ctx context.Context, feedURL, feedType string, tlsInsecure bool) (string, error) {
	d, err := r.DiscoverFeed(ctx, feedURL, feedType, tlsInsecure)
	return d.Title, err
}

// DiscoverFeed resolves feedURL to the address that serves the feed (HTML
// pages are scanned for feed links, permanent redirects are followed) and
// its title. Bridge URLs are returned unchanged with the bridge's title.
func (r *TitleResolver) DiscoverFeed(ctx context.Context, feedURL, feedType string, tlsInsecure bool) (Discovery, error) {
	if r == nil {
		return Discovery{}, fmt.Errorf("title resolver not configured")
	}
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return Discovery{}, fmt.Errorf("empty feed url")
	}
	ft := NormalizeFeedType(feedType)
	if ft == "" {
		ft = DetectFeedTypeFromURL(feedURL)
	}
	switch ft {
	case FeedTypeTelegram:
		if r.telegram == nil {
			return Discovery{}, fmt.Errorf("telegram handler not configured")
		}
		title, err := r.telegram.DiscoverChannelTitle(ctx, feedURL)
		return Discovery{FeedURL: feedURL, Title: title}, err
	case FeedTypePage:
		if r.page == nil {
			return Discovery{}, fmt.Errorf("page handler not configured")
		}
		title, err := r.page.DiscoverTitle(ctx, feedURL, tlsInsecure)
		return Discovery{FeedURL: feedURL, Title: title}, err
	default:
		if r.rss == nil {
			return Discovery{}, fmt.Errorf("rss fetcher not configured")
		}
		return r.rss.Discover(ctx, feedURL, false, tlsInsecure)
	}
}
