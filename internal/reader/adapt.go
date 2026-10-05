package reader

import (
	"context"

	"rssam/internal/bridge/v1"
)

// Adapt registers a contract bridge in the core: its State lives under the
// handler's name in the feed's BridgeState, next to the other bridges'
// documents, and a nil Response.State keeps the previous one.
func Adapt(h bridge.Handler) Handler {
	return adapted{h: h}
}

type adapted struct {
	h bridge.Handler
}

func (a adapted) Name() string { return a.h.Name() }

func (a adapted) DetectFeedType(feedURL string) string { return a.h.DetectFeedType(feedURL) }

func (a adapted) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	name := a.h.Name()
	prev := req.BridgeState.Raw(name)
	res, err := a.h.Fetch(ctx, bridge.Request{
		FeedURL:       req.FeedURL,
		FeedType:      req.FeedType,
		UserAgent:     req.UserAgent,
		ETag:          req.ETag,
		LastModified:  req.LastModified,
		State:         prev,
		FetchViaProxy: req.FetchViaProxy,
		TLSInsecure:   req.TLSInsecure,
	})
	state := res.State
	if state == nil {
		state = prev
	}
	return FetchResponse{
		Entries:      res.Entries,
		ETag:         res.ETag,
		LastModified: res.LastModified,
		NotModified:  res.NotModified,
		BridgeState:  req.BridgeState.With(name, state),
		MinNextCheck: res.MinNextCheck,
		NewURL:       res.NewURL,
	}, err
}
