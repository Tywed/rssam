package reader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// Discovery is the outcome of resolving a user-supplied URL to a feed: the
// URL that actually serves the feed (after permanent redirects or HTML
// autodiscovery) and its title.
type Discovery struct {
	FeedURL string
	Title   string
}

// maxDiscoverHTMLBytes bounds how much of an HTML page is read looking for
// <link rel="alternate">; the head of any real page fits many times over.
const maxDiscoverHTMLBytes = 1 << 20

// maxDiscoverCandidates caps feed candidates tried per page (declared links
// first, then well-known paths), so one page costs at most this many requests.
const maxDiscoverCandidates = 6

// wellKnownFeedPaths are tried on the page's origin when it declares no feed links.
var wellKnownFeedPaths = []string{"/feed", "/rss", "/feed.xml", "/rss.xml", "/atom.xml", "/index.xml"}

// ErrNoFeedFound is returned when the URL is an HTML page without any
// resolvable feed.
var ErrNoFeedFound = errors.New("no RSS/Atom feed found at this address")

// Discover fetches pageURL: a feed document is returned as is; an HTML page
// is scanned for <link rel="alternate" type="application/rss+xml|atom+xml|feed+json">
// and the first candidate that parses wins, falling back to well-known paths.
// A permanent redirect chain replaces FeedURL with its destination.
func (f *RSSFetcher) Discover(ctx context.Context, pageURL string, useProxy, tlsInsecure bool) (Discovery, error) {
	pageURL = strings.TrimSpace(pageURL)
	if pageURL == "" {
		return Discovery{}, fmt.Errorf("empty feed url")
	}
	d, candidates, err := f.discoverOnce(ctx, pageURL, useProxy, tlsInsecure)
	if err != nil {
		return Discovery{}, err
	}
	if d.FeedURL != "" {
		return d, nil
	}
	tried := 0
	var lastErr error
	for _, c := range candidates {
		if tried >= maxDiscoverCandidates {
			break
		}
		tried++
		cd, _, cerr := f.discoverOnce(ctx, c, useProxy, tlsInsecure)
		if cerr == nil && cd.FeedURL != "" {
			return cd, nil
		}
		if cerr != nil {
			lastErr = cerr
		}
	}
	if lastErr != nil {
		return Discovery{}, fmt.Errorf("%w (last candidate: %v)", ErrNoFeedFound, lastErr)
	}
	return Discovery{}, ErrNoFeedFound
}

// discoverOnce fetches one URL. It returns either a Discovery (feed found),
// or candidate feed URLs extracted from an HTML page (FeedURL empty).
func (f *RSSFetcher) discoverOnce(ctx context.Context, rawURL string, useProxy, tlsInsecure bool) (Discovery, []string, error) {
	if f.guard != nil {
		if err := f.guard.ValidateURL(rawURL); err != nil {
			return Discovery{}, nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Discovery{}, nil, fmt.Errorf("create request: %w", err)
	}
	if f.userAgent != "" {
		req.Header.Set("User-Agent", f.userAgent)
	}
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json, text/xml, text/html;q=0.9, */*;q=0.8")

	client, err := f.httpClient(useProxy, tlsInsecure)
	if err != nil {
		return Discovery{}, nil, err
	}
	redirects := trackPermanentRedirects(client)
	resp, err := client.Do(req)
	if err != nil {
		return Discovery{}, nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Discovery{}, nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	finalURL := rawURL
	if u := redirects.permanentDestination(resp); u != "" {
		finalURL = u
	}

	head := make([]byte, 4096)
	n, _ := io.ReadFull(resp.Body, head)
	head = head[:n]
	if looksLikeHTML(head, resp.Header.Get("Content-Type")) {
		base := resp.Request.URL
		body := io.LimitReader(io.MultiReader(bytes.NewReader(head), resp.Body), maxDiscoverHTMLBytes)
		links := feedLinksFromHTML(body, base)
		if len(links) == 0 {
			links = wellKnownFeedURLs(base)
		}
		return Discovery{}, links, nil
	}
	parsed, err := parseLimitedFeed(f.parser, io.MultiReader(bytes.NewReader(head), resp.Body))
	if err != nil {
		return Discovery{}, nil, fmt.Errorf("parse feed: %w", err)
	}
	title := strings.TrimSpace(parsed.Title)
	if title == "" {
		title = finalURL
	}
	return Discovery{FeedURL: finalURL, Title: title}, nil, nil
}

// looksLikeHTML sniffs the body first (feeds are often served as text/html)
// and trusts the Content-Type only when the body gives no signal.
func looksLikeHTML(head []byte, contentType string) bool {
	trimmed := bytes.TrimLeft(head, " \t\r\n\xef\xbb\xbf")
	lower := bytes.ToLower(trimmed)
	switch {
	case bytes.HasPrefix(lower, []byte("<?xml")), bytes.HasPrefix(lower, []byte("<rss")),
		bytes.HasPrefix(lower, []byte("<feed")), bytes.HasPrefix(lower, []byte("<rdf")),
		bytes.HasPrefix(lower, []byte("{")):
		return false
	case bytes.HasPrefix(lower, []byte("<!doctype html")), bytes.HasPrefix(lower, []byte("<html")):
		return true
	}
	return strings.Contains(strings.ToLower(contentType), "text/html")
}

var feedLinkTypes = map[string]bool{
	"application/rss+xml":   true,
	"application/atom+xml":  true,
	"application/feed+json": true,
	"application/json":      true,
}

// feedLinksFromHTML returns absolute http(s) URLs of <link rel="alternate">
// feed declarations in document order; scanning stops at </head> or <body>.
func feedLinksFromHTML(r io.Reader, base *url.URL) []string {
	z := html.NewTokenizer(r)
	var out []string
	seen := map[string]bool{}
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			tag := string(name)
			if tag == "body" {
				return out
			}
			if tag != "link" || !hasAttr {
				continue
			}
			var rel, typ, href string
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case "rel":
					rel = string(v)
				case "type":
					typ = string(v)
				case "href":
					href = string(v)
				}
				if !more {
					break
				}
			}
			if !hasToken(strings.ToLower(rel), "alternate") || !feedLinkTypes[strings.ToLower(strings.TrimSpace(typ))] {
				continue
			}
			abs := resolveHTTPURL(base, href)
			if abs == "" || seen[abs] {
				continue
			}
			seen[abs] = true
			out = append(out, abs)
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) == "head" {
				return out
			}
		}
	}
}

func hasToken(list, want string) bool {
	for _, t := range strings.Fields(list) {
		if t == want {
			return true
		}
	}
	return false
}

func resolveHTTPURL(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" || base == nil {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" || abs.Host == "" {
		return ""
	}
	abs.Fragment = ""
	return abs.String()
}

func wellKnownFeedURLs(base *url.URL) []string {
	if base == nil || base.Host == "" {
		return nil
	}
	out := make([]string, 0, len(wellKnownFeedPaths))
	for _, p := range wellKnownFeedPaths {
		out = append(out, (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: p}).String())
	}
	return out
}

// redirectTracker records whether every hop of a redirect chain was
// permanent (301/308), in which case the destination may replace the stored URL.
type redirectTracker struct {
	hops      int
	permanent bool
}

// trackPermanentRedirects installs a CheckRedirect on client (a per-request
// copy, see clientWithIsolatedCookieJar) and returns the tracker.
func trackPermanentRedirects(client *http.Client) *redirectTracker {
	t := &redirectTracker{permanent: true}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		t.hops++
		if req.Response == nil || (req.Response.StatusCode != http.StatusMovedPermanently && req.Response.StatusCode != http.StatusPermanentRedirect) {
			t.permanent = false
		}
		return nil
	}
	return t
}

// permanentDestination returns the final URL when the whole chain was
// permanent and stays on http(s), otherwise "".
func (t *redirectTracker) permanentDestination(resp *http.Response) string {
	if t == nil || t.hops == 0 || !t.permanent || resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	u := resp.Request.URL
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}
