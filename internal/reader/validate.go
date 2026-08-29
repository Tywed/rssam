package reader

import (
	"fmt"
	"net/url"
	"strings"

	"rssam/internal/reader/dzen"
	maxbridge "rssam/internal/reader/max"
	maxstatbridge "rssam/internal/reader/maxstat"
	"rssam/internal/reader/rutube"
	"rssam/internal/reader/smotrim"
	"rssam/internal/reader/telegram"
	vkbridge "rssam/internal/reader/vk"
	"rssam/internal/ssrf"
)

var bridgeFeedSchemes = map[string]struct{}{
	"vk-search":       {},
	"maxstat-search":  {},
	"dzen-news":       {},
	"dzen-search":   {},
	"rutube-person": {},
	"smotrim":       {},
	"smotrim-brand": {},
}

// IsBridgeSchemeURL reports whether feedURL uses an internal bridge pseudo-scheme
// (not fetched directly; resolved to http(s) by the bridge handler).
func IsBridgeSchemeURL(feedURL string) bool {
	u, err := url.Parse(strings.TrimSpace(feedURL))
	if err != nil || u.Scheme == "" {
		return false
	}
	_, ok := bridgeFeedSchemes[strings.ToLower(u.Scheme)]
	return ok
}

// ValidateFeedURL checks a subscription URL before storing it.
// Bridge pseudo-schemes skip SSRF; http(s) URLs are validated by the SSRF guard.
func ValidateFeedURL(feedURL string, guard *ssrf.Guard) error {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return fmt.Errorf("feed_url is required")
	}
	if IsBridgeSchemeURL(feedURL) {
		return ValidateBridgeFeedURL(feedURL, DetectFeedTypeFromURL(feedURL))
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("feed_url must be a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("feed_url must use http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("feed_url must be a valid URL")
	}
	if guard != nil {
		if err := guard.ValidateURL(feedURL); err != nil {
			return err
		}
	}
	return nil
}

// FeedTypeLabel returns a human-readable feed type name.
func FeedTypeLabel(feedType string) string {
	switch NormalizeFeedType(feedType) {
	case FeedTypeTelegram:
		return "Telegram"
	case FeedTypeMax:
		return "Max"
	case FeedTypeMaxstat:
		return "MaxStat"
	case FeedTypeVKSearch:
		return "VK"
	case FeedTypeRutube:
		return "Rutube"
	case FeedTypeDzenNews:
		return "Dzen News"
	case FeedTypeSmotrim:
		return "Smotrim"
	default:
		return "RSS/Atom"
	}
}

// ValidateBridgeFeedURL checks that feedURL matches the detected bridge feed type.
// RSS/Atom URLs are not probed here; use DiscoverTitle or RSS fetch for that.
func ValidateBridgeFeedURL(feedURL, feedType string) error {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return fmt.Errorf("URL обязателен")
	}
	ft := NormalizeFeedType(feedType)
	if ft == "" {
		ft = DetectFeedTypeFromURL(feedURL)
	}
	switch ft {
	case FeedTypeTelegram:
		if !telegram.DetectFeedURL(feedURL) {
			return fmt.Errorf("не удалось распознать Telegram-канал в URL")
		}
	case FeedTypeMax:
		if ch, ok := maxbridge.ParseChannelFromFeedURL(feedURL); !ok || ch == "" {
			return fmt.Errorf("не удалось распознать канал Max в URL")
		}
	case FeedTypeMaxstat:
		if !maxstatbridge.DetectFeedURL(feedURL) {
			return fmt.Errorf("не удалось распознать поисковый запрос MaxStat в URL")
		}
	case FeedTypeVKSearch:
		if !vkbridge.DetectFeedURL(feedURL) {
			return fmt.Errorf("не удалось распознать VK-поиск в URL")
		}
	case FeedTypeRutube:
		if _, ok := rutube.ParseChannelIDFromFeedURL(feedURL); !ok {
			return fmt.Errorf("не удалось распознать канал Rutube в URL")
		}
	case FeedTypeDzenNews:
		if _, ok := dzen.ParseQueryFromFeedURL(feedURL); !ok {
			return fmt.Errorf("не удалось распознать поисковый запрос Dzen News в URL")
		}
	case FeedTypeSmotrim:
		if _, ok := smotrim.ParseOptionsFromFeedURL(feedURL); !ok {
			return fmt.Errorf("не удалось распознать бренд Smotrim в URL")
		}
	}
	return nil
}
