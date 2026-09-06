package reader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"
	"time"

	"rssam/internal/model"
	"rssam/internal/ssrf"
	"rssam/internal/storage"

	"github.com/mmcdole/gofeed"
)

type FetchResult struct {
	Entries      []storage.CreateEntryParams
	ETag         string
	LastModified string
	NotModified  bool
}

// MaxFeedBodyBytes caps how much of a feed document is read, so one hostile
// or misconfigured feed cannot exhaust memory.
const MaxFeedBodyBytes = 16 << 20

// ErrFeedTooLarge is returned when the feed body exceeds MaxFeedBodyBytes.
var ErrFeedTooLarge = errors.New("feed body exceeds size limit")

func parseLimitedFeed(parser *gofeed.Parser, r io.Reader) (*gofeed.Feed, error) {
	lr := &io.LimitedReader{R: r, N: MaxFeedBodyBytes + 1}
	parsed, err := parser.Parse(lr)
	if lr.N == 0 {
		return nil, ErrFeedTooLarge
	}
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

type RSSFetcher struct {
	client    *http.Client
	proxy     *proxyClientCache
	parser    *gofeed.Parser
	userAgent string
	guard     *ssrf.Guard
}

func NewRSSFetcher(client *http.Client, userAgent string, guard *ssrf.Guard, fetchViaProxyURL string) *RSSFetcher {
	if client == nil {
		client = http.DefaultClient
	}
	return &RSSFetcher{
		client:    client,
		proxy:     newProxyClientCache(client, fetchViaProxyURL),
		parser:    gofeed.NewParser(),
		userAgent: strings.TrimSpace(userAgent),
		guard:     guard,
	}
}

func (f *RSSFetcher) Fetch(ctx context.Context, feedURL, etag, lastModified string, useProxy, tlsInsecure bool) (FetchResult, error) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return FetchResult{}, fmt.Errorf("empty feed url")
	}
	if f.guard != nil {
		if err := f.guard.ValidateURL(feedURL); err != nil {
			return FetchResult{}, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("create request: %w", err)
	}
	if f.userAgent != "" {
		req.Header.Set("User-Agent", f.userAgent)
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json, text/xml, */*")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	client, err := f.httpClient(useProxy, tlsInsecure)
	if err != nil {
		return FetchResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return FetchResult{
			NotModified:  true,
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
		}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return FetchResult{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	parsed, err := parseLimitedFeed(f.parser, resp.Body)
	if err != nil {
		return FetchResult{}, fmt.Errorf("parse feed: %w", err)
	}

	out := make([]storage.CreateEntryParams, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		out = append(out, normalizeItem(it))
	}

	return FetchResult{
		Entries:      out,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		NotModified:  false,
	}, nil
}

// DiscoverTitle fetches a feed once and returns channel/site title (TT-RSS-style auto naming).
func (f *RSSFetcher) DiscoverTitle(ctx context.Context, feedURL string, useProxy, tlsInsecure bool) (string, error) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return "", fmt.Errorf("empty feed url")
	}
	if f.guard != nil {
		if err := f.guard.ValidateURL(feedURL); err != nil {
			return "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	if f.userAgent != "" {
		req.Header.Set("User-Agent", f.userAgent)
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json, text/xml, */*")

	client, err := f.httpClient(useProxy, tlsInsecure)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	parsed, err := parseLimitedFeed(f.parser, resp.Body)
	if err != nil {
		return "", fmt.Errorf("parse feed: %w", err)
	}
	title := strings.TrimSpace(parsed.Title)
	if title == "" {
		return "", fmt.Errorf("feed has no title")
	}
	return title, nil
}

func (f *RSSFetcher) httpClient(useProxy, tlsInsecure bool) (*http.Client, error) {
	timeout := 15 * time.Second
	if f.client != nil && f.client.Timeout > 0 {
		timeout = f.client.Timeout
	}
	var base *http.Client
	var err error
	if f.proxy != nil {
		base, err = f.proxy.client(useProxy, tlsInsecure, f.guard, f.client, timeout)
		if err != nil {
			return nil, err
		}
	} else if f.guard != nil {
		base = f.guard.HTTPClientForFetch(timeout, tlsInsecure)
	} else {
		base = f.client
	}
	return clientWithIsolatedCookieJar(base), nil
}

// clientWithIsolatedCookieJar copies base and attaches a fresh jar so Set-Cookie
// is sent on redirects. Cached transports stay shared; cookies are not.
func clientWithIsolatedCookieJar(base *http.Client) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	c := *base
	jar, err := cookiejar.New(nil)
	if err != nil {
		return &c
	}
	c.Jar = jar
	return &c
}

func normalizeItem(it *gofeed.Item) storage.CreateEntryParams {
	var (
		author *string
		pub    = it.PublishedParsed
	)
	if pub == nil {
		pub = it.UpdatedParsed
	}
	if it.Author != nil && strings.TrimSpace(it.Author.Name) != "" {
		v := strings.TrimSpace(it.Author.Name)
		author = &v
	}

	url := strings.TrimSpace(it.Link)
	if url == "" {
		url = strings.TrimSpace(it.GUID)
	}
	url = model.NormalizeURL(url)

	content := strings.TrimSpace(it.Content)
	if content == "" {
		content = strings.TrimSpace(it.Description)
	}
	content = SanitizeHTML(content)

	title := strings.TrimSpace(it.Title)
	hash := model.DedupHashFromURL(url)

	var encs []storage.CreateEnclosureParams
	for _, enc := range it.Enclosures {
		if enc == nil {
			continue
		}
		encURL := strings.TrimSpace(enc.URL)
		if encURL == "" {
			continue
		}
		var size int64
		if enc.Length != "" {
			if n, err := strconv.ParseInt(strings.TrimSpace(enc.Length), 10, 64); err == nil {
				size = n
			}
		}
		encs = append(encs, storage.CreateEnclosureParams{
			URL:      encURL,
			Size:     size,
			MIMEType: strings.TrimSpace(enc.Type),
		})
	}

	return storage.CreateEntryParams{
		Title:       title,
		URL:         url,
		Content:     content,
		Author:      author,
		PublishedAt: pub,
		Hash:        hash,
		Status:      storage.EntryStatusUnread,
		Enclosures:  encs,
	}
}
