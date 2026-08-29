package reader

import (
	"context"

	"rssam/internal/reader/smotrim"
)

type smotrimHandler struct {
	inner *smotrim.Handler
}

func newSmotrimHandler(inner *smotrim.Handler) Handler {
	return &smotrimHandler{inner: inner}
}

func (h *smotrimHandler) Name() string {
	if h == nil || h.inner == nil {
		return FeedTypeSmotrim
	}
	return h.inner.Name()
}

func (h *smotrimHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *smotrimHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	st := smotrim.FetchState{}
	if req.BridgeState.Smotrim != nil {
		st.BrandID = req.BridgeState.Smotrim.BrandID
		st.Limit = req.BridgeState.Smotrim.Limit
		st.VideoType = req.BridgeState.Smotrim.VideoType
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, st)
	return FetchResponse{
		Entries: res.Entries,
		BridgeState: BridgeState{
			Smotrim: &SmotrimBridgeState{
				BrandID:   res.State.BrandID,
				Limit:     res.State.Limit,
				VideoType: res.State.VideoType,
			},
		},
	}, err
}
