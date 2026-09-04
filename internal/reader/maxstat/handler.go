package maxstat

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"rssam/internal/model"
	"rssam/internal/storage"
)

// Handler fetches posts via maxstat.ru search API.
type Handler struct {
	client *Client
	cfg    Config

	mu          sync.Mutex
	rateLimited map[string]time.Time
}

func NewHandler(client *Client, cfg Config) *Handler {
	return &Handler{
		client:      client,
		cfg:         cfg.withDefaults(),
		rateLimited: make(map[string]time.Time),
	}
}

func (h *Handler) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	h.cfg = cfg
	if h.client != nil {
		h.client.UpdateConfig(cfg)
	}
}

func (h *Handler) Name() string { return feedTypeMaxstat }

func (h *Handler) DetectFeedType(feedURL string) string {
	if DetectFeedURL(feedURL) {
		return feedTypeMaxstat
	}
	return ""
}

type FetchState struct {
	Query            string
	ApiToken         string
	LastEndTime      int64
	RateLimitedUntil *time.Time
}

type FetchResult struct {
	Entries []storage.CreateEntryParams
	State   FetchState
}

func (h *Handler) Fetch(ctx context.Context, feedURL string, st FetchState) (FetchResult, error) {
	if h == nil || h.client == nil {
		return FetchResult{}, errMissingToken
	}

	query, err := ResolveQuery(feedURL, st.Query)
	if err != nil {
		return FetchResult{}, err
	}
	st.Query = query
	// Preserve per-feed api_token from bridge_state (RSS-Bridge style).

	now := time.Now().UTC()
	if st.RateLimitedUntil != nil && now.Before(*st.RateLimitedUntil) {
		return FetchResult{State: st}, fmt.Errorf("maxstat: rate limited until %s", st.RateLimitedUntil.Format(time.RFC3339))
	}
	if h.isQueryRateLimited(query, now) {
		return FetchResult{State: st}, fmt.Errorf("maxstat: rate limited (in-memory)")
	}

	lastEnd := st.LastEndTime
	body, status, err := h.client.Search(ctx, SearchParams{
		Query:       query,
		Limit:       h.cfg.DefaultLimit,
		AccessToken: st.ApiToken,
	})
	if err != nil {
		if status == 429 {
			until := now.Add(h.cfg.RateLimitCooldown)
			st.RateLimitedUntil = &until
			h.setQueryRateLimit(query, until)
		}
		return FetchResult{State: st}, err
	}

	entries := make([]storage.CreateEntryParams, 0, len(body.Posts))
	for _, post := range body.Posts {
		pub, ok := parsePublishedAt(post.PublishedAt)
		if !ok {
			continue
		}
		if ShouldSkipPost(pub.Unix(), lastEnd) {
			continue
		}
		entries = append(entries, entryFromPost(post, pub))
	}

	if maxPub := MaxPublishedUnix(body.Posts, parsePublishedAt); maxPub > st.LastEndTime {
		st.LastEndTime = maxPub
	}
	st.RateLimitedUntil = nil

	return FetchResult{
		Entries: entries,
		State:   st,
	}, nil
}

func entryFromPost(post post, pub time.Time) storage.CreateEntryParams {
	entryURL := model.NormalizeURL(strings.TrimSpace(post.URL))
	title := TitleFromPost(post)
	content := BuildContentHTML(post)
	utc := pub.UTC()
	hash := model.DedupHashFromString(DedupKey(post.ID))

	return storage.CreateEntryParams{
		Title:       title,
		URL:         entryURL,
		Content:     content,
		PublishedAt: &utc,
		Hash:        hash,
		Status:      storage.EntryStatusUnread,
	}
}

func parsePublishedAt(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
	}
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func (h *Handler) isQueryRateLimited(query string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for q, until := range h.rateLimited {
		if !now.Before(until) {
			delete(h.rateLimited, q)
		}
	}
	until, ok := h.rateLimited[query]
	return ok && now.Before(until)
}

func (h *Handler) setQueryRateLimit(query string, until time.Time) {
	h.mu.Lock()
	h.rateLimited[query] = until
	h.mu.Unlock()
}
