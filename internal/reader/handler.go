package reader

import (
	"context"
	"errors"
	"time"

	"rssam/internal/reader/dzen"
	maxbridge "rssam/internal/reader/max"
	maxstatbridge "rssam/internal/reader/maxstat"
	"rssam/internal/reader/rutube"
	"rssam/internal/reader/smotrim"
	"rssam/internal/reader/telegram"
	vkbridge "rssam/internal/reader/vk"
	"rssam/internal/storage"
)

var ErrNoHandlerFound = errors.New("no feed handler found")

// FetchRequest describes parameters for fetching entries from a source.
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
	Entries      []storage.CreateEntryParams
	ETag         string
	LastModified string
	NotModified  bool
	BridgeState  BridgeState
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

// Fetch resolves a handler and fetches entries.
func (r *HandlerRegistry) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	h, err := r.FindHandler(req.FeedURL, req.FeedType)
	if err != nil {
		return FetchResponse{}, err
	}
	res, err := h.Fetch(ctx, req)
	if err != nil {
		return res, err
	}
	SanitizeEntries(res.Entries)
	return res, nil
}

const (
	FeedTypeRSS      = "rss"
	FeedTypeAtom     = "atom"
	FeedTypeJSON     = "json"
	FeedTypeTelegram = "telegram"
	FeedTypeVK       = "vk"
	FeedTypeVKSearch = "vk_search"
	FeedTypeMax      = "max"
	FeedTypeMaxstat  = "maxstat"
	FeedTypeRutube   = "rutube"
	FeedTypeDzenNews = "dzen_news"
	FeedTypeSmotrim  = "smotrim"
	FeedTypeCustom   = "custom"
)

// NormalizeFeedType returns a canonical feed type or empty string.
func NormalizeFeedType(t string) string {
	switch t {
	case FeedTypeRSS, FeedTypeAtom, FeedTypeJSON, FeedTypeTelegram, FeedTypeVK, FeedTypeVKSearch, FeedTypeMax, FeedTypeMaxstat, FeedTypeRutube, FeedTypeDzenNews, FeedTypeSmotrim, FeedTypeCustom:
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
	if rutube.DetectFeedURL(feedURL) {
		return FeedTypeRutube
	}
	if dzen.DetectFeedURL(feedURL) {
		return FeedTypeDzenNews
	}
	if smotrim.DetectFeedURL(feedURL) {
		return FeedTypeSmotrim
	}
	return FeedTypeRSS
}

// RetryAtError is a fetch result that should be retried later without counting as a poll failure.
type RetryAtError interface {
	error
	RetryAt() time.Time
}

func RetryAt(err error) (time.Time, bool) {
	if e, ok := errors.AsType[RetryAtError](err); ok {
		at := e.RetryAt()
		if !at.IsZero() {
			return at, true
		}
	}
	return time.Time{}, false
}
