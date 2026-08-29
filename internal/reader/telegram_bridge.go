package reader

import (
	"context"

	"rssam/internal/reader/telegram"
)

type telegramHandler struct {
	inner *telegram.Handler
}

func newTelegramHandler(inner *telegram.Handler) Handler {
	return &telegramHandler{inner: inner}
}

func (h *telegramHandler) Name() string {
	if h == nil || h.inner == nil {
		return FeedTypeTelegram
	}
	return h.inner.Name()
}

func (h *telegramHandler) DetectFeedType(feedURL string) string {
	if h == nil || h.inner == nil {
		return ""
	}
	return h.inner.DetectFeedType(feedURL)
}

func (h *telegramHandler) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if h == nil || h.inner == nil {
		return FetchResponse{}, ErrNoHandlerFound
	}
	var override *telegram.BridgeOverride
	if req.BridgeState.Telegram != nil {
		tg := req.BridgeState.Telegram
		override = &telegram.BridgeOverride{
			UseProxy:        tg.UseProxy,
			ProxyServiceURL: tg.ProxyServiceURL,
			ProxyTargetURL:  tg.ProxyTargetURL,
			StaticProxy:     tg.StaticProxy,
			MaxPages:        tg.MaxPages,
		}
	}
	res, err := h.inner.Fetch(ctx, req.FeedURL, override)
	return FetchResponse{
		Entries:     res.Entries,
		BridgeState: req.BridgeState,
	}, err
}
