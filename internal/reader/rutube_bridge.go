package reader

import (
	"context"

	"rssam/internal/reader/rutube"
)

type rutubeHandler struct {
	inner *rutube.Handler
}

func newRutubeHandler(inner *rutube.Handler) Handler {
	return &rutubeHandler{inner: inner}
}

func (h *rutubeHandler) Name() string {
	if h == nil || h.inner == nil {
		return FeedTypeRutube
	}
	return h.inner.Name()
}

func (h *rutubeHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *rutubeHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	st := rutube.FetchState{}
	if req.BridgeState.Rutube != nil {
		st.ChannelID = req.BridgeState.Rutube.ChannelID
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, st)
	out := FetchResponse{
		Entries: res.Entries,
		BridgeState: BridgeState{
			Rutube: &RutubeBridgeState{
				ChannelID: res.State.ChannelID,
			},
		},
	}
	return out, err
}
