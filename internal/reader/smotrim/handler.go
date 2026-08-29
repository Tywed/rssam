package smotrim

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rssam/internal/model"
	"rssam/internal/storage"
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
	BrandID   string
	Limit     int
	VideoType string
}

type FetchResult struct {
	Entries   []storage.CreateEntryParams
	State     FetchState
	FeedTitle string
	FeedURI   string
}

func (h *Handler) Fetch(ctx context.Context, feedURL string, st FetchState) (FetchResult, error) {
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
	entries := make([]storage.CreateEntryParams, 0, len(items))
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

func entryFromVideoItem(item VideoItem, now time.Time) storage.CreateEntryParams {
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

	return storage.CreateEntryParams{
		Title:       title,
		URL:         entryURL,
		Content:     BuildContentHTML(item),
		Author:      authorPtr,
		PublishedAt: pub,
		Hash:        DedupHash(item.PublicID),
		Status:      storage.EntryStatusUnread,
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
