package reader

import (
	"context"

	maxbridge "rssam/internal/reader/max"
)

type maxHandler struct {
	inner *maxbridge.Handler
}

func newMaxHandler(inner *maxbridge.Handler) Handler {
	return &maxHandler{inner: inner}
}

func (h *maxHandler) Name() string {
	if h == nil || h.inner == nil {
		return FeedTypeMax
	}
	return h.inner.Name()
}

func (h *maxHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *maxHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	st := maxbridge.FetchState{}
	if req.BridgeState.Max != nil {
		st.LastEndTimeMs = req.BridgeState.Max.LastEndTimeMs
		st.RateLimitedUntil = req.BridgeState.Max.RateLimitedUntil
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, st)
	out := FetchResponse{
		Entries: res.Entries,
		BridgeState: BridgeState{
			Max: &MaxBridgeState{
				LastEndTimeMs:    res.State.LastEndTimeMs,
				RateLimitedUntil: res.State.RateLimitedUntil,
			},
		},
	}
	return out, err
}
