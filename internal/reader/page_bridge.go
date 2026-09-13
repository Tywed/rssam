package reader

import (
	"context"

	"rssam/internal/reader/page"
)

type pageHandler struct {
	inner *page.Handler
}

func newPageHandler(inner *page.Handler) Handler {
	return &pageHandler{inner: inner}
}

func (h *pageHandler) Name() string { return FeedTypePage }

func (h *pageHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *pageHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	st := page.State{}
	if req.BridgeState.Page != nil {
		st.Hash = req.BridgeState.Page.Hash
		st.Text = req.BridgeState.Page.Text
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, req.UserAgent, st, req.TLSInsecure)
	if err != nil {
		return FetchResponse{BridgeState: req.BridgeState}, err
	}
	for i := range res.Entries {
		res.Entries[i].Content = SanitizeHTML(res.Entries[i].Content)
	}
	out := FetchResponse{
		Entries: res.Entries,
		// Unchanged page: the poll writes nothing new; the refresher only
		// stamps last_checked_at. The snapshot is re-sent unchanged.
		NotModified: !res.Modified,
		BridgeState: BridgeState{Page: &PageBridgeState{Hash: res.State.Hash, Text: res.State.Text}},
	}
	return out, nil
}
