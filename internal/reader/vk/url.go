package vk

import (
	"net/url"
	"regexp"
	"strings"
)

var spaceCollapse = regexp.MustCompile(`\s+`)

const feedTypeVKSearch = "vk_search"

// NormalizeQuery lowercases and collapses whitespace (cursor key).
func NormalizeQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	q = spaceCollapse.ReplaceAllString(q, " ")
	return strings.ToLower(q)
}

// ParseQueryFromFeedURL extracts the search query from supported feed URLs.
func ParseQueryFromFeedURL(feedURL string) (string, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return "", false
	}
	if q, ok := parseVKSearchScheme(feedURL); ok {
		return q, true
	}
	u, err := url.Parse(feedURL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "vk-search":
		q := strings.TrimSpace(u.Host)
		if p := strings.Trim(u.Path, "/"); p != "" {
			if q != "" {
				q += "/" + p
			} else {
				q = p
			}
		}
		if q == "" {
			q = strings.TrimSpace(u.Query().Get("q"))
		}
		if dec, err := url.QueryUnescape(q); err == nil && dec != "" {
			q = dec
		}
		q = NormalizeQuery(q)
		return q, q != ""
	case "http", "https":
		host := strings.ToLower(u.Hostname())
		if host != "vk.com" && host != "www.vk.com" && !strings.HasSuffix(host, ".vk.com") {
			return "", false
		}
		path := strings.Trim(strings.ToLower(u.Path), "/")
		if path != "feed" {
			return "", false
		}
		if strings.ToLower(strings.TrimSpace(u.Query().Get("section"))) != "search" {
			return "", false
		}
		q := strings.TrimSpace(u.Query().Get("q"))
		if q == "" {
			return "", false
		}
		return NormalizeQuery(q), true
	default:
		return "", false
	}
}

func parseVKSearchScheme(feedURL string) (string, bool) {
	const prefix = "vk-search://"
	if !strings.HasPrefix(strings.ToLower(feedURL), prefix) {
		return "", false
	}
	raw := strings.TrimSpace(feedURL[len(prefix):])
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "?") {
		if u, err := url.Parse("http://local" + raw); err == nil {
			if q := strings.TrimSpace(u.Query().Get("q")); q != "" {
				if dec, err := url.QueryUnescape(q); err == nil {
					q = dec
				}
				q = NormalizeQuery(q)
				return q, q != ""
			}
		}
	}
	if dec, err := url.QueryUnescape(raw); err == nil && dec != "" {
		raw = dec
	}
	q := NormalizeQuery(raw)
	return q, q != ""
}

// DetectFeedURL reports whether feedURL is a VK newsfeed search subscription.
func DetectFeedURL(feedURL string) bool {
	_, ok := ParseQueryFromFeedURL(feedURL)
	return ok
}

// ResolveQuery returns the normalized query from bridge override or feed URL.
func ResolveQuery(feedURL string, bridgeQuery string) (string, error) {
	if q := NormalizeQuery(bridgeQuery); q != "" {
		return q, nil
	}
	q, ok := ParseQueryFromFeedURL(feedURL)
	if !ok || q == "" {
		return "", errInvalidFeedURL
	}
	return q, nil
}
