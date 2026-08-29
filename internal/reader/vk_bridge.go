package reader

import (
	"context"

	"rssam/internal/reader/vk"
)

type vkSearchHandler struct {
	inner *vk.Handler
}

func newVKSearchHandler(inner *vk.Handler) Handler {
	return &vkSearchHandler{inner: inner}
}

func (h *vkSearchHandler) Name() string {
	if h == nil || h.inner == nil {
		return FeedTypeVKSearch
	}
	return h.inner.Name()
}

func (h *vkSearchHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *vkSearchHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	st := vk.FetchState{}
	if req.BridgeState.VKSearch != nil {
		st.Query = req.BridgeState.VKSearch.Q
		st.LastEndTime = req.BridgeState.VKSearch.LastEndTime
		st.RateLimitedUntil = req.BridgeState.VKSearch.RateLimitedUntil
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, st)
	out := FetchResponse{
		Entries: res.Entries,
		BridgeState: BridgeState{
			VKSearch: &VKSearchBridgeState{
				Q:                res.State.Query,
				LastEndTime:      res.State.LastEndTime,
				RateLimitedUntil: res.State.RateLimitedUntil,
			},
		},
	}
	return out, err
}
