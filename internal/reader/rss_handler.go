package reader

import (
	"context"

	"rssam/internal/bridge/v1"
)

// rssState is the RSS handler's document in the feed's BridgeState: the
// hash of the last body, so a source that never answers 304 is still
// recognised as unchanged without parsing.
type rssState struct {
	Body string `json:"body,omitempty"`
}

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
	var prev rssState
	_ = decodeRSSState(req.BridgeState, &prev)
	res, err := h.Fetcher.FetchWith(ctx, FetchParams{
		FeedURL:      req.FeedURL,
		ETag:         req.ETag,
		LastModified: req.LastModified,
		BodyHash:     prev.Body,
		UseProxy:     req.FetchViaProxy,
		TLSInsecure:  req.TLSInsecure,
	})
	if err != nil {
		return FetchResponse{}, err
	}
	state := req.BridgeState
	if res.BodyHash != "" && res.BodyHash != prev.Body {
		state = state.With(FeedTypeRSS, bridge.EncodeState(rssState{Body: res.BodyHash}))
	}
	return FetchResponse{
		Entries:      res.Entries,
		ETag:         res.ETag,
		LastModified: res.LastModified,
		NotModified:  res.NotModified,
		BridgeState:  state,
		MinNextCheck: res.MinNextCheck,
		NewURL:       res.NewURL,
	}, nil
}

func decodeRSSState(state BridgeState, into *rssState) error {
	return bridge.DecodeState(state.Raw(FeedTypeRSS), into)
}
