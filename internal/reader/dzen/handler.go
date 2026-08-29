package dzen

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rssam/internal/model"
	"rssam/internal/storage"
)

// Handler fetches Dzen News search results.
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

func (h *Handler) Name() string { return feedTypeDzenNews }

func (h *Handler) DetectFeedType(feedURL string) string {
	if DetectFeedURL(feedURL) {
		return feedTypeDzenNews
	}
	return ""
}

type FetchState struct {
	Query string
}

type FetchResult struct {
	Entries   []storage.CreateEntryParams
	State     FetchState
	FeedTitle string
	FeedURI   string
}

func (h *Handler) Fetch(ctx context.Context, feedURL string, st FetchState) (FetchResult, error) {
	if h == nil || h.client == nil {
		return FetchResult{}, fmt.Errorf("dzen: handler is not configured")
	}

	query, err := ResolveQuery(feedURL, st.Query)
	if err != nil {
		return FetchResult{}, err
	}
	st.Query = query

	items, err := h.client.SearchNews(ctx, query)
	if err != nil {
		return FetchResult{State: st}, err
	}

	now := time.Now()
	entries := make([]storage.CreateEntryParams, 0, len(items))
	for _, item := range items {
		entries = append(entries, entryFromNewsItem(item, now))
	}

	return FetchResult{
		Entries:   entries,
		State:     st,
		FeedTitle: FeedTitle(query),
		FeedURI:   SearchPageURL(h.cfg.SearchURL, query),
	}, nil
}

func entryFromNewsItem(item NewsItem, now time.Time) storage.CreateEntryParams {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "News"
	}
	entryURL := model.NormalizeURL(item.URL)

	var authorPtr *string
	if author := strings.TrimSpace(item.Author); author != "" {
		authorPtr = &author
	}

	var pub *time.Time
	if item.PubDateUnix > 0 {
		t := time.Unix(item.PubDateUnix, 0).UTC()
		pub = &t
	} else if t := ParsePublishedTime(item.TimeText, now); !t.IsZero() {
		utc := t.UTC()
		pub = &utc
	}

	hash := DedupHash(item.DocID, entryURL)

	return storage.CreateEntryParams{
		Title:       title,
		URL:         entryURL,
		Content:     BuildContentHTML(item),
		Author:      authorPtr,
		PublishedAt: pub,
		Hash:        hash,
		Status:      storage.EntryStatusUnread,
	}
}

func FeedTitle(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Dzen News"
	}
	return "Dzen: " + query
}

func DedupHash(docID, entryURL string) string {
	if id := strings.TrimSpace(docID); id != "" {
		return model.DedupHashFromString(id)
	}
	return model.DedupHashFromURL(entryURL)
}
