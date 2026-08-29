package dzen

import (
	"net/url"
	"regexp"
	"strings"
)

const feedTypeDzenNews = "dzen_news"

var spaceCollapse = regexp.MustCompile(`\s+`)

// NormalizeQuery trims and collapses whitespace in a search query.
func NormalizeQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	return spaceCollapse.ReplaceAllString(q, " ")
}

// ParseQueryFromFeedURL extracts the search query from supported feed URLs.
func ParseQueryFromFeedURL(feedURL string) (string, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return "", false
	}
	if q, ok := parseDzenNewsScheme(feedURL); ok {
		return q, true
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "dzen.ru" && host != "www.dzen.ru" && !strings.HasSuffix(host, ".dzen.ru") {
		return "", false
	}
	path := strings.Trim(strings.ToLower(u.Path), "/")
	if path != "news/search" {
		return "", false
	}
	q := strings.TrimSpace(u.Query().Get("text"))
	if q == "" {
		q = strings.TrimSpace(u.Query().Get("q"))
	}
	if q == "" {
		return "", false
	}
	q = NormalizeQuery(q)
	return q, q != ""
}

func parseDzenNewsScheme(feedURL string) (string, bool) {
	for _, prefix := range []string{"dzen-news://", "dzen-search://"} {
		if !strings.HasPrefix(strings.ToLower(feedURL), prefix) {
			continue
		}
		raw := strings.TrimSpace(feedURL[len(prefix):])
		if raw == "" {
			return "", false
		}
		if dec, err := url.QueryUnescape(raw); err == nil && dec != "" {
			raw = dec
		}
		q := NormalizeQuery(raw)
		return q, q != ""
	}
	return "", false
}

// DetectFeedURL reports whether feedURL is a Dzen News search subscription.
func DetectFeedURL(feedURL string) bool {
	_, ok := ParseQueryFromFeedURL(feedURL)
	return ok
}

// SearchPageURL builds the public Dzen news search URL for a query.
func SearchPageURL(baseURL, query string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultSearchURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		u, _ = url.Parse(defaultSearchURL)
	}
	q := u.Query()
	q.Set("issue_tld", "ru")
	q.Set("sortby", "date")
	q.Set("text", NormalizeQuery(query))
	u.RawQuery = q.Encode()
	return u.String()
}

// ResolveQuery returns query from bridge override or feed URL.
func ResolveQuery(feedURL, bridgeQuery string) (string, error) {
	if q := NormalizeQuery(bridgeQuery); q != "" {
		return q, nil
	}
	q, ok := ParseQueryFromFeedURL(feedURL)
	if !ok || q == "" {
		return "", errInvalidFeedURL
	}
	return q, nil
}
