package reader

import (
	"context"
	"errors"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/reader/dzen"
	"rssam/internal/reader/dzenchannel"
	maxbridge "rssam/internal/reader/max"
	maxstatbridge "rssam/internal/reader/maxstat"
	"rssam/internal/reader/okgroup"
	"rssam/internal/reader/page"
	"rssam/internal/reader/rutube"
	"rssam/internal/reader/smotrim"
	"rssam/internal/reader/telegram"
	vkbridge "rssam/internal/reader/vk"
)

var ErrNoHandlerFound = errors.New("no feed handler found")

// FetchRequest describes parameters for fetching entries from a source.
// Contract bridges (bridge.Handler) receive it as bridge.Request via Adapt.
type FetchRequest struct {
	FeedURL       string
	FeedType      string
	UserAgent     string
	ETag          string
	LastModified  string
	BridgeState   BridgeState
	FetchViaProxy bool
	TLSInsecure   bool
}

// FetchResponse is the result of a source fetch.
type FetchResponse struct {
	Entries      []bridge.Entry
	ETag         string
	LastModified string
	NotModified  bool
	BridgeState  BridgeState
	// MinNextCheck: see FetchResult.MinNextCheck.
	MinNextCheck time.Time
	// NewURL: see FetchResult.NewURL.
	NewURL string
}

// Handler fetches entries from a single source type.
type Handler interface {
	Name() string
	DetectFeedType(feedURL string) string
	Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error)
}

// HandlerRegistry resolves handlers by feed URL / type.
type HandlerRegistry struct {
	handlers []Handler
}

func NewHandlerRegistry(handlers ...Handler) *HandlerRegistry {
	return &HandlerRegistry{handlers: handlers}
}

func (r *HandlerRegistry) Register(h Handler) {
	if h == nil {
		return
	}
	r.handlers = append(r.handlers, h)
}

// FindHandler returns the first handler that recognizes feedURL.
// RSS is expected to be registered last (fallback).
func (r *HandlerRegistry) FindHandler(feedURL, feedType string) (Handler, error) {
	ft := NormalizeFeedType(feedType)
	if ft != "" && ft != FeedTypeRSS {
		for _, h := range r.handlers {
			if h.Name() == ft {
				return h, nil
			}
		}
	}
	for _, h := range r.handlers {
		if detected := h.DetectFeedType(feedURL); detected != "" {
			return h, nil
		}
	}
	return nil, ErrNoHandlerFound
}

// Fetch resolves a handler and fetches entries. Content comes back as the
// source sent it; the refresher sanitizes what it is about to store or
// deliver (SanitizeEntries), after the item-set and dedup checks have
// dropped the items it already knows.
func (r *HandlerRegistry) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	h, err := r.FindHandler(req.FeedURL, req.FeedType)
	if err != nil {
		return FetchResponse{}, err
	}
	return h.Fetch(ctx, req)
}

const (
	FeedTypeRSS      = "rss"
	FeedTypeAtom     = "atom"
	FeedTypeJSON     = "json"
	FeedTypeTelegram = "telegram"
	FeedTypeVK       = "vk"
	FeedTypeVKSearch = "vk_search"
	// FeedTypeVKGroup is served by a contract bridge (vk.GroupHandler).
	FeedTypeVKGroup  = "vk_group"
	FeedTypeMax      = "max"
	FeedTypeMaxstat  = "maxstat"
	FeedTypeRutube   = "rutube"
	FeedTypeDzenNews = "dzen_news"
	// FeedTypeDzenChannel is served by a contract bridge (dzenchannel).
	FeedTypeDzenChannel = "dzen_channel"
	// FeedTypeOKGroup is served by a contract bridge (okgroup).
	FeedTypeOKGroup = "ok_group"
	FeedTypeSmotrim = "smotrim"
	FeedTypePage    = "page"
	FeedTypeCustom  = "custom"
	// FeedTypeInbound is never fetched: entries are POSTed to /hooks/{id}.
	FeedTypeInbound = "inbound"
)

// ErrBridgeNotConfigured is a bridge that needs a token the operator has not
// set (vk_group without VK_ACCESS_TOKEN).
var ErrBridgeNotConfigured = errors.New("bridge is not configured: access token missing")

// NormalizeFeedType returns a canonical feed type or empty string.
func NormalizeFeedType(t string) string {
	switch t {
	case FeedTypeRSS, FeedTypeAtom, FeedTypeJSON, FeedTypeTelegram, FeedTypeVK, FeedTypeVKSearch, FeedTypeVKGroup, FeedTypeMax, FeedTypeMaxstat, FeedTypeRutube, FeedTypeDzenNews, FeedTypeDzenChannel, FeedTypeOKGroup, FeedTypeSmotrim, FeedTypePage, FeedTypeCustom, FeedTypeInbound:
		return t
	default:
		return ""
	}
}

// DetectFeedTypeFromURL infers feed type from a subscription URL.
func DetectFeedTypeFromURL(feedURL string) string {
	if telegram.DetectFeedURL(feedURL) {
		return FeedTypeTelegram
	}
	if ch, ok := maxbridge.ParseChannelFromFeedURL(feedURL); ok && ch != "" {
		return FeedTypeMax
	}
	if maxstatbridge.DetectFeedURL(feedURL) {
		return FeedTypeMaxstat
	}
	if vkbridge.DetectFeedURL(feedURL) {
		return FeedTypeVKSearch
	}
	if vkbridge.DetectGroupFeedURL(feedURL) {
		return FeedTypeVKGroup
	}
	if rutube.DetectFeedURL(feedURL) {
		return FeedTypeRutube
	}
	if dzen.DetectFeedURL(feedURL) {
		return FeedTypeDzenNews
	}
	if dzenchannel.Detect(feedURL) {
		return FeedTypeDzenChannel
	}
	if okgroup.Detect(feedURL) {
		return FeedTypeOKGroup
	}
	if smotrim.DetectFeedURL(feedURL) {
		return FeedTypeSmotrim
	}
	if page.DetectFeedURL(feedURL) {
		return FeedTypePage
	}
	return FeedTypeRSS
}

// RetryAtError is a fetch result that should be retried later without counting as a poll failure.
type RetryAtError = bridge.RetryAtError

func RetryAt(err error) (time.Time, bool) {
	return bridge.RetryAt(err)
}
