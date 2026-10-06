package smotrim

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/model"
)

// Handler fetches Smotrim brand videos.
type Handler struct {
	client *Client
	cfg    Config
}

func NewHandler(client *Client, cfg Config) *Handler {
	return &Handler{client: client, cfg: cfg.withDefaults()}
}

func (h *Handler) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	h.cfg = cfg
	if h.client != nil {
		h.client.UpdateConfig(cfg)
	}
}

func (h *Handler) Name() string { return feedTypeSmotrim }

func (h *Handler) DetectFeedType(feedURL string) string {
	if DetectFeedURL(feedURL) {
		return feedTypeSmotrim
	}
	return ""
}

type FetchState struct {
	BrandID   string `json:"brand_id,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	VideoType string `json:"video_type,omitempty"`
}

type FetchResult struct {
	Entries   []bridge.Entry
	State     FetchState
	FeedTitle string
	FeedURI   string
}

// Fetch implements bridge.Handler; State is FetchState.
func (h *Handler) Fetch(ctx context.Context, req bridge.Request) (bridge.Response, error) {
	var st FetchState
	_ = bridge.DecodeState(req.State, &st)
	res, err := h.fetch(ctx, req.FeedURL, st)
	return bridge.Response{Entries: res.Entries, State: bridge.EncodeState(res.State)}, err
}

func (h *Handler) fetch(ctx context.Context, feedURL string, st FetchState) (FetchResult, error) {
	if h == nil || h.client == nil {
		return FetchResult{}, fmt.Errorf("smotrim: handler is not configured")
	}

	resolved, err := ResolveOptions(feedURL, st)
	if err != nil {
		return FetchResult{State: st}, err
	}

	items, brandTitle, err := h.client.ListBrandVideos(ctx, resolved)
	if err != nil {
		return FetchResult{State: resolved}, err
	}

	now := time.Now()
	entries := make([]bridge.Entry, 0, len(items))
	for _, item := range items {
		entries = append(entries, entryFromVideoItem(item, now))
	}

	return FetchResult{
		Entries:   entries,
		State:     resolved,
		FeedTitle: FeedTitle(brandTitle, resolved.BrandID),
		FeedURI:   BrandPageURL(h.cfg.BrandBaseURL, resolved.BrandID),
	}, nil
}

func entryFromVideoItem(item VideoItem, now time.Time) bridge.Entry {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "Видео"
	}
	entryURL := model.NormalizeURL(VideoPageURL(item.PublicID))

	var authorPtr *string
	if author := strings.TrimSpace(item.BrandTitle); author != "" {
		authorPtr = &author
	}

	var pub *time.Time
	if !item.PublishedAt.IsZero() {
		t := item.PublishedAt.UTC()
		pub = &t
	} else if t := ParsePublishedTime(item.DateText, now); !t.IsZero() {
		utc := t.UTC()
		pub = &utc
	}

	return bridge.Entry{
		Title:       title,
		URL:         entryURL,
		Content:     BuildContentHTML(item),
		Author:      authorPtr,
		PublishedAt: pub,
		Hash:        DedupHash(item.PublicID),
	}
}

func FeedTitle(brandTitle, brandID string) string {
	brandTitle = strings.TrimSpace(brandTitle)
	if brandTitle != "" {
		return "Smotrim: " + brandTitle
	}
	if id := strings.TrimSpace(brandID); id != "" {
		return "Smotrim: brand " + id
	}
	return "Smotrim"
}

func DedupHash(publicID int64) string {
	return model.DedupHashFromString(fmt.Sprintf("smotrim-video-%d", publicID))
}
