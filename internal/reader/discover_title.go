package reader

import (
	"context"
	"fmt"
	"strings"

	"rssam/internal/bridge/v1"
	"rssam/internal/proxy"
	"rssam/internal/reader/page"
	"rssam/internal/reader/telegram"
)

// TitleResolver discovers feed titles when the user leaves the title empty (TT-RSS-style).
type TitleResolver struct {
	rss      *RSSFetcher
	telegram *telegram.Handler
	page     *page.Handler
	contract []bridge.Handler
}

// NewTitleResolver builds a title resolver. If tg is non-nil, Telegram title
// discovery reuses the poller's proxy client instead of allocating a second
// one. contract are the registry's bridge.Handler implementations; those that
// are a bridge.TitleDiscoverer name their feeds.
func NewTitleResolver(cfg RegistryConfig, tg *telegram.Handler, contract []bridge.Handler) (*TitleResolver, error) {
	rss := NewRSSFetcher(cfg.HTTPClient, cfg.UserAgent, cfg.SSRFGuard, cfg.FetchViaProxyURL)
	pg := page.NewHandler(cfg.HTTPClient, cfg.SSRFGuard, cfg.UserAgent)
	if tg != nil {
		return &TitleResolver{rss: rss, telegram: tg, page: pg, contract: contract}, nil
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
	return &TitleResolver{rss: rss, telegram: created, page: pg, contract: contract}, nil
}

func (r *TitleResolver) contractDiscoverer(feedType string) (bridge.TitleDiscoverer, bool) {
	if r == nil {
		return nil, false
	}
	for _, h := range r.contract {
		if h.Name() != feedType {
			continue
		}
		d, ok := h.(bridge.TitleDiscoverer)
		return d, ok
	}
	return nil, false
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
		if d, ok := r.contractDiscoverer(ft); ok {
			title, err := d.DiscoverTitle(ctx, feedURL)
			return Discovery{FeedURL: feedURL, Title: title}, err
		}
		if r.rss == nil {
			return Discovery{}, fmt.Errorf("rss fetcher not configured")
		}
		return r.rss.Discover(ctx, feedURL, false, tlsInsecure)
	}
}
