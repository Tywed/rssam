package reader

import (
	"context"

	"rssam/internal/reader/dzen"
)

type dzenNewsHandler struct {
	inner *dzen.Handler
}

func newDzenNewsHandler(inner *dzen.Handler) Handler {
	return &dzenNewsHandler{inner: inner}
}

func (h *dzenNewsHandler) Name() string {
	if h == nil || h.inner == nil {
		return FeedTypeDzenNews
	}
	return h.inner.Name()
}

func (h *dzenNewsHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *dzenNewsHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	st := dzen.FetchState{}
	if req.BridgeState.DzenNews != nil {
		st.Query = req.BridgeState.DzenNews.Q
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, st)
	out := FetchResponse{
		Entries: res.Entries,
		BridgeState: BridgeState{
			DzenNews: &DzenNewsBridgeState{
				Q: res.State.Query,
			},
		},
	}
	return out, err
}
