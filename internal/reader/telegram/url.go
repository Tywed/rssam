package telegram

import (
	"net/url"
	"regexp"
	"strings"
)

var detectURLRegex = regexp.MustCompile(`(?i)^https?://(?:(?:t|telegram)\.me/(?:s/)?([\w]+)|([\w]+)\.t\.me/?)$`)

// NormalizeUsername strips leading @ and whitespace.
func NormalizeUsername(username string) string {
	return strings.TrimLeft(strings.TrimSpace(username), "@")
}

// ParseUsernameFromFeedURL extracts channel username from t.me / telegram.me URLs.
func ParseUsernameFromFeedURL(feedURL string) (string, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return "", false
	}
	if m := detectURLRegex.FindStringSubmatch(feedURL); len(m) > 0 {
		if m[1] != "" {
			return NormalizeUsername(m[1]), true
		}
		if len(m) > 2 && m[2] != "" {
			return NormalizeUsername(m[2]), true
		}
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	switch host {
	case "t.me", "telegram.me":
		path := strings.Trim(u.Path, "/")
		if path == "" {
			return "", false
		}
		parts := strings.Split(path, "/")
		if len(parts) >= 2 && parts[0] == "s" {
			return NormalizeUsername(parts[1]), true
		}
		if len(parts) == 1 && parts[0] != "s" {
			return NormalizeUsername(parts[0]), true
		}
	case "":
	default:
		if strings.HasSuffix(host, ".t.me") {
			sub := strings.TrimSuffix(host, ".t.me")
			if sub != "" {
				return NormalizeUsername(sub), true
			}
		}
	}
	return "", false
}

// DetectFeedURL reports whether feedURL is a public Telegram channel preview URL.
func DetectFeedURL(feedURL string) bool {
	_, ok := ParseUsernameFromFeedURL(feedURL)
	return ok
}

// PreviewURL returns https://t.me/s/{username}.
func PreviewURL(username string) string {
	username = NormalizeUsername(username)
	return "https://t.me/s/" + url.PathEscape(username)
}
