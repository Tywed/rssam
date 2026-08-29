package reader

import (
	"context"

	"rssam/internal/reader/maxstat"
)

type maxstatHandler struct {
	inner *maxstat.Handler
}

func newMaxstatHandler(inner *maxstat.Handler) Handler {
	return &maxstatHandler{inner: inner}
}

func (h *maxstatHandler) Name() string {
	if h == nil || h.inner == nil {
		return FeedTypeMaxstat
	}
	return h.inner.Name()
}

func (h *maxstatHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *maxstatHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	st := maxstat.FetchState{}
	if req.BridgeState.Maxstat != nil {
		st.Query = req.BridgeState.Maxstat.Q
		st.ApiToken = req.BridgeState.Maxstat.ApiToken
		st.LastEndTime = req.BridgeState.Maxstat.LastEndTime
		st.RateLimitedUntil = req.BridgeState.Maxstat.RateLimitedUntil
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, st)
	return FetchResponse{
		Entries: res.Entries,
		BridgeState: BridgeState{
			Maxstat: &MaxstatBridgeState{
				Q:                res.State.Query,
				ApiToken:         res.State.ApiToken,
				LastEndTime:      res.State.LastEndTime,
				RateLimitedUntil: res.State.RateLimitedUntil,
			},
		},
	}, err
}
