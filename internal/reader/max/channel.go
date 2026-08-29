package max

import (
	"net/url"
	"strings"
)

// ParseChannelFromFeedURL extracts channel name from https://max.ru/{channel} URLs.
func ParseChannelFromFeedURL(feedURL string) (channel string, ok bool) {
	feedURL = strings.TrimSpace(feedURL)
	u, err := url.Parse(feedURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host != "max.ru" && host != "www.max.ru" {
		return "", false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", false
	}
	parts := strings.Split(path, "/")
	switch len(parts) {
	case 1:
		return parts[0], parts[0] != ""
	case 2:
		if parts[0] == "channel" || parts[0] == "c" {
			return parts[1], parts[1] != ""
		}
	}
	return "", false
}
