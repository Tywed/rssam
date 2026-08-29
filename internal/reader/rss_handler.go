package reader

import (
	"context"
)

// RSSHandler adapts RSSFetcher to the Handler interface.
type RSSHandler struct {
	Fetcher *RSSFetcher
}

func NewRSSHandler(fetcher *RSSFetcher) *RSSHandler {
	return &RSSHandler{Fetcher: fetcher}
}

func (h *RSSHandler) Name() string { return FeedTypeRSS }

func (h *RSSHandler) DetectFeedType(feedURL string) string {
	if DetectFeedTypeFromURL(feedURL) != FeedTypeRSS {
		return ""
	}
	if h == nil || h.Fetcher == nil {
		return FeedTypeRSS
	}
	return FeedTypeRSS
}

func (h *RSSHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.Fetcher == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	res, err := h.Fetcher.Fetch(ctx, req.FeedURL, req.ETag, req.LastModified, req.FetchViaProxy, req.TLSInsecure)
	if err != nil {
		return FetchResponse{}, err
	}
	return FetchResponse{
		Entries:      res.Entries,
		ETag:         res.ETag,
		LastModified: res.LastModified,
		NotModified:  res.NotModified,
		BridgeState:  req.BridgeState,
	}, nil
}
