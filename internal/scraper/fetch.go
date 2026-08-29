package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"rssam/internal/reader"
	"rssam/internal/ssrf"
)

// Fetcher downloads HTML pages with SSRF protection and size limits.
type Fetcher struct {
	client       *http.Client
	proxy        *proxyClientCache
	userAgent    string
	maxBodyBytes int64
	guard        *ssrf.Guard
}

type proxyClientCache struct {
	mu     sync.Mutex
	base   *http.Client
	proxy  string
	cached *http.Client
}

type Options struct {
	HTTPClient       *http.Client
	UserAgent        string
	MaxBodyBytes     int64
	SSRFGuard        *ssrf.Guard
	FetchViaProxyURL string
}

func NewFetcher(opts Options) *Fetcher {
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	maxBytes := opts.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20 // 1 MiB
	}
	return &Fetcher{
		client:       client,
		proxy:        newProxyClientCache(client, opts.FetchViaProxyURL),
		userAgent:    strings.TrimSpace(opts.UserAgent),
		maxBodyBytes: maxBytes,
		guard:        opts.SSRFGuard,
	}
}

func newProxyClientCache(base *http.Client, proxyURL string) *proxyClientCache {
	return &proxyClientCache{base: base, proxy: strings.TrimSpace(proxyURL)}
}

func (c *proxyClientCache) client(useProxy bool) (*http.Client, error) {
	if !useProxy || c == nil || c.proxy == "" {
		return c.base, nil
	}
	// lazy init with mutex would be better; single-threaded scrape is ok for MVP
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached != nil {
		return c.cached, nil
	}
	u, err := url.Parse(c.proxy)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	transport := c.base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	rt, ok := transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("unsupported transport for proxy")
	}
	cp := rt.Clone()
	cp.Proxy = http.ProxyURL(u)
	c.cached = &http.Client{Timeout: c.base.Timeout, Transport: cp, CheckRedirect: c.base.CheckRedirect}
	return c.cached, nil
}

// FetchHTML downloads a page and returns its HTML body (truncated to max size).
func (f *Fetcher) FetchHTML(ctx context.Context, pageURL, feedUserAgent string, useProxy bool) (string, error) {
	pageURL = strings.TrimSpace(pageURL)
	if pageURL == "" {
		return "", fmt.Errorf("empty url")
	}
	if f.guard != nil {
		if err := f.guard.ValidateURL(pageURL); err != nil {
			return "", err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	ua := strings.TrimSpace(feedUserAgent)
	if ua == "" {
		ua = f.userAgent
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")

	client := f.client
	if f.proxy != nil {
		var errClient error
		client, errClient = f.proxy.client(useProxy)
		if errClient != nil {
			return "", errClient
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, f.maxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > f.maxBodyBytes {
		return "", fmt.Errorf("response body exceeds max size (%d bytes)", f.maxBodyBytes)
	}
	return string(body), nil
}

// ScrapePage fetches HTML, extracts content, applies rewrite rules.
func (f *Fetcher) ScrapePage(ctx context.Context, pageURL, scraperRules, rewriteRules, feedUserAgent string, useProxy bool) (content string, err error) {
	html, err := f.FetchHTML(ctx, pageURL, feedUserAgent, useProxy)
	if err != nil {
		return "", err
	}
	content, err = ExtractContent(html, scraperRules)
	if err != nil {
		return "", err
	}
	content = ApplyRewriteRules(content, rewriteRules)
	return reader.SanitizeHTML(content), nil
}
