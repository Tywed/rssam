package rutube

import (
	"net/url"
	"regexp"
	"strings"
)

const feedTypeRutube = "rutube"

var (
	personPathRegex  = regexp.MustCompile(`(?i)^/video/person/(\d+)/?$`)
	channelPathRegex = regexp.MustCompile(`(?i)^/channel/(\d+)/?$`)
)

// ParseChannelIDFromFeedURL extracts a Rutube person (channel) id from supported URLs.
func ParseChannelIDFromFeedURL(feedURL string) (string, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return "", false
	}
	if id, ok := parseRutubeScheme(feedURL); ok {
		return id, true
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host != "rutube.ru" && host != "www.rutube.ru" && !strings.HasSuffix(host, ".rutube.ru") {
		return "", false
	}
	if m := personPathRegex.FindStringSubmatch(u.Path); len(m) > 1 {
		return m[1], true
	}
	if m := channelPathRegex.FindStringSubmatch(u.Path); len(m) > 1 {
		return m[1], true
	}
	if c := strings.TrimSpace(u.Query().Get("c")); c != "" && isDigits(c) {
		return c, true
	}
	return "", false
}

func parseRutubeScheme(feedURL string) (string, bool) {
	const prefix = "rutube-person://"
	if !strings.HasPrefix(strings.ToLower(feedURL), prefix) {
		return "", false
	}
	id := strings.Trim(strings.TrimSpace(feedURL[len(prefix):]), "/")
	if id == "" || !isDigits(id) {
		return "", false
	}
	return id, true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// DetectFeedURL reports whether feedURL is a Rutube person subscription.
func DetectFeedURL(feedURL string) bool {
	_, ok := ParseChannelIDFromFeedURL(feedURL)
	return ok
}

// PersonPageURL returns the public person page URL.
func PersonPageURL(channelID string) string {
	channelID = strings.TrimSpace(channelID)
	return "https://rutube.ru/video/person/" + channelID + "/"
}

// VideoURL builds a canonical video page URL from id.
func VideoURL(videoID string) string {
	videoID = strings.TrimSpace(videoID)
	return "https://rutube.ru/video/" + videoID + "/"
}

// ResolveChannelID returns channel id from bridge override or feed URL.
func ResolveChannelID(feedURL string, bridgeChannelID string) (string, error) {
	if id := strings.TrimSpace(bridgeChannelID); id != "" && isDigits(id) {
		return id, nil
	}
	id, ok := ParseChannelIDFromFeedURL(feedURL)
	if !ok || id == "" {
		return "", errInvalidFeedURL
	}
	return id, nil
}
