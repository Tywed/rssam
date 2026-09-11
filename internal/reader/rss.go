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
	// MinNextCheck is the earliest time the source wants to be asked again
	// (Cache-Control max-age, Expires or RSS <ttl>); zero when it said nothing.
	MinNextCheck time.Time
}

// ErrFeedGone is returned for HTTP 410: the source removed the feed for
// good, so retrying only costs both sides traffic.
var ErrFeedGone = errors.New("feed gone (410)")

// rateLimitedError carries Retry-After from a 429/503 response.
type rateLimitedError struct {
	status int
	until  time.Time
}

// Error carries no timestamp so identical rate-limit rows coalesce in the
// poll log.
func (e *rateLimitedError) Error() string {
	return fmt.Sprintf("rate limited by source (status %d, Retry-After honoured)", e.status)
}

func (e *rateLimitedError) RetryAt() time.Time { return e.until }

// maxServerDelay caps what a source may ask for through Retry-After,
// max-age, Expires or <ttl>: a misconfigured header must not park a feed for
// a week.
const maxServerDelay = 24 * time.Hour

// parseRetryAfter reads Retry-After as delay seconds or an HTTP date.
// Zero when absent or unparsable.
func parseRetryAfter(h string, now time.Time) time.Time {
	h = strings.TrimSpace(h)
	if h == "" {
		return time.Time{}
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs <= 0 {
			return time.Time{}
		}
		return now.Add(min(time.Duration(secs)*time.Second, maxServerDelay))
	}
	if t, err := http.ParseTime(h); err == nil && t.After(now) {
		return now.Add(min(t.Sub(now), maxServerDelay))
	}
	return time.Time{}
}

// cacheFreshUntil derives the earliest sensible next poll from response
// caching headers: Cache-Control max-age (s-maxage preferred) wins over
// Expires. Anything under a minute is noise and ignored.
func cacheFreshUntil(h http.Header, now time.Time) time.Time {
	var d time.Duration
	for directive := range strings.SplitSeq(h.Get("Cache-Control"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(directive), "=")
		if !ok {
			continue
		}
		k = strings.ToLower(k)
		if k != "max-age" && k != "s-maxage" {
			continue
		}
		secs, err := strconv.Atoi(strings.Trim(v, `"`))
		if err != nil || secs <= 0 {
			continue
		}
		if k == "s-maxage" {
			d = time.Duration(secs) * time.Second
			break
		}
		if d == 0 {
			d = time.Duration(secs) * time.Second
		}
	}
	if d == 0 {
		if exp := strings.TrimSpace(h.Get("Expires")); exp != "" {
			if t, err := http.ParseTime(exp); err == nil && t.After(now) {
				d = t.Sub(now)
			}
		}
	}
	if d < time.Minute {
		return time.Time{}
	}
	return now.Add(min(d, maxServerDelay))
}

// feedTTL reads RSS 2.0 <ttl> (minutes). gofeed keeps it only on the
// RSS-specific feed, which the translator does not surface, so it is parsed
// out of the raw XML before the body is handed to gofeed.
func feedTTL(head []byte, now time.Time) time.Time {
	i := strings.Index(string(head), "<ttl>")
	if i < 0 {
		return time.Time{}
	}
	rest := head[i+len("<ttl>"):]
	j := strings.Index(string(rest), "</ttl>")
	if j < 0 || j > 10 {
		return time.Time{}
	}
	mins, err := strconv.Atoi(strings.TrimSpace(string(rest[:j])))
	if err != nil || mins <= 0 {
		return time.Time{}
	}
	return now.Add(min(time.Duration(mins)*time.Minute, maxServerDelay))
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

	now := time.Now().UTC()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return FetchResult{
			NotModified:  true,
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			MinNextCheck: cacheFreshUntil(resp.Header, now),
		}, nil
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		if until := parseRetryAfter(resp.Header.Get("Retry-After"), now); !until.IsZero() {
			return FetchResult{}, &rateLimitedError{status: resp.StatusCode, until: until}
		}
		return FetchResult{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	case http.StatusGone:
		return FetchResult{}, ErrFeedGone
	case http.StatusOK:
	default:
		return FetchResult{}, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// <ttl> sits in <channel> before the items; 4 KB of head is plenty.
	head := make([]byte, 4096)
	n, _ := io.ReadFull(resp.Body, head)
	head = head[:n]
	parsed, err := parseLimitedFeed(f.parser, io.MultiReader(strings.NewReader(string(head)), resp.Body))
	if err != nil {
		return FetchResult{}, fmt.Errorf("parse feed: %w", err)
	}

	out := make([]storage.CreateEntryParams, 0, len(parsed.Items))
	for _, it := range parsed.Items {
		out = append(out, normalizeItem(it))
	}

	minNext := cacheFreshUntil(resp.Header, now)
	if ttl := feedTTL(head, now); ttl.After(minNext) {
		minNext = ttl
	}
	return FetchResult{
		Entries:      out,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		NotModified:  false,
		MinNextCheck: minNext,
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
