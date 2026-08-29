package maxstat

import (
	"net/url"
	"regexp"
	"strings"
)

var spaceCollapse = regexp.MustCompile(`\s+`)

const feedTypeMaxstat = "maxstat"

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
	if q, ok := parseMaxstatSearchScheme(feedURL); ok {
		return q, true
	}
	u, err := url.Parse(feedURL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "maxstat-search":
		q := strings.TrimSpace(u.Host)
		if p := strings.Trim(u.Path, "/"); p != "" {
			if q != "" {
				q += "/" + p
			} else {
				q = p
			}
		}
		if q == "" {
			q = strings.TrimSpace(u.Query().Get("search"))
		}
		if dec, err := url.QueryUnescape(q); err == nil && dec != "" {
			q = dec
		}
		q = NormalizeQuery(q)
		return q, q != ""
	case "http", "https":
		host := strings.ToLower(u.Hostname())
		if host != "maxstat.ru" && host != "www.maxstat.ru" {
			return "", false
		}
		path := strings.Trim(strings.ToLower(u.Path), "/")
		if path != "posts" {
			return "", false
		}
		q := strings.TrimSpace(u.Query().Get("search"))
		if q == "" {
			return "", false
		}
		return NormalizeQuery(q), true
	default:
		return "", false
	}
}

func parseMaxstatSearchScheme(feedURL string) (string, bool) {
	const prefix = "maxstat-search://"
	if !strings.HasPrefix(strings.ToLower(feedURL), prefix) {
		return "", false
	}
	raw := strings.TrimSpace(feedURL[len(prefix):])
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(raw, "?") {
		if u, err := url.Parse("http://local" + raw); err == nil {
			if q := strings.TrimSpace(u.Query().Get("search")); q != "" {
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

// DetectFeedURL reports whether feedURL is a MaxStat search subscription.
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

// SearchPageURL builds the public search page URL for a query.
func SearchPageURL(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "https://maxstat.ru"
	}
	return "https://maxstat.ru/posts?search=" + url.QueryEscape(query)
}

// FeedTitle returns a display title for a search feed.
func FeedTitle(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "MaxStat"
	}
	return "MaxStat: «" + query + "»"
}
