package vk

import (
	"context"
	"fmt"
	"sync"
	"time"

	"rssam/internal/model"
	"rssam/internal/storage"
)

// Handler fetches posts via VK newsfeed.search.
type Handler struct {
	client *Client
	cfg    Config

	mu          sync.Mutex
	rateLimited map[string]time.Time // normalized query -> until
}

func NewHandler(client *Client, cfg Config) *Handler {
	return &Handler{
		client:      client,
		cfg:         cfg.withDefaults(),
		rateLimited: make(map[string]time.Time),
	}
}

// UpdateConfig replaces global VK bridge settings at runtime.
func (h *Handler) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	h.cfg = cfg
	if h.client != nil {
		h.client.UpdateConfig(cfg)
	}
}

func (h *Handler) Name() string { return feedTypeVKSearch }

func (h *Handler) DetectFeedType(feedURL string) string {
	if DetectFeedURL(feedURL) {
		return feedTypeVKSearch
	}
	return ""
}

// FetchState is per-feed bridge cursor for VK search.
type FetchState struct {
	Query            string
	LastEndTime      int64
	RateLimitedUntil *time.Time
}

// FetchResult is the outcome of a VK search poll.
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

	now := time.Now().UTC()
	if st.RateLimitedUntil != nil && now.Before(*st.RateLimitedUntil) {
		return FetchResult{State: st}, fmt.Errorf("vk: rate limited until %s", st.RateLimitedUntil.Format(time.RFC3339))
	}
	if h.isQueryRateLimited(query, now) {
		return FetchResult{State: st}, fmt.Errorf("vk: rate limited (in-memory)")
	}

	lastEnd := st.LastEndTime
	win := ComputeTimeWindow(lastEnd, h.cfg.DefaultLookback, h.cfg.Overlap, now)

	body, apiErr, err := h.client.Search(ctx, SearchParams{
		Query:     query,
		Count:     h.cfg.DefaultCount,
		StartTime: win.StartTime,
		EndTime:   win.EndTime,
	})
	if err != nil {
		return FetchResult{}, err
	}
	if apiErr != nil {
		if apiErr.ErrorCode == 6 {
			until := now.Add(h.cfg.RateLimitCooldown)
			st.RateLimitedUntil = &until
			h.setQueryRateLimit(query, until)
			return FetchResult{State: st}, fmt.Errorf("vk: rate limited (error %d)", apiErr.ErrorCode)
		}
		return FetchResult{}, fmt.Errorf("vk api error %d: %s", apiErr.ErrorCode, apiErr.ErrorMsg)
	}

	ownerNames := buildOwnerNames(body.Profiles, body.Groups)
	entries := make([]storage.CreateEntryParams, 0, len(body.Items))
	for _, post := range body.Items {
		if ShouldSkipPost(int64(post.Date), lastEnd) {
			continue
		}
		entries = append(entries, entryFromPost(post, ownerNames))
	}

	st.LastEndTime = win.EndTime
	st.RateLimitedUntil = nil

	return FetchResult{
		Entries: entries,
		State:   st,
	}, nil
}

func entryFromPost(post wallPost, ownerNames map[int]string) storage.CreateEntryParams {
	entryURL := model.NormalizeURL(postURL(post.OwnerID, post.ID))
	title := titleFromPost(post)
	content := BuildContentHTML(post, ownerNames)
	author := postAuthor(post, ownerNames)

	var pub *time.Time
	if post.Date > 0 {
		t := time.Unix(int64(post.Date), 0).UTC()
		pub = &t
	}

	hash := model.DedupHashFromString(DedupKey(post.OwnerID, post.ID))

	var authorPtr *string
	if author != "" {
		authorPtr = &author
	}

	return storage.CreateEntryParams{
		Title:       title,
		URL:         entryURL,
		Content:     content,
		Author:      authorPtr,
		PublishedAt: pub,
		Hash:        hash,
		Status:      storage.EntryStatusUnread,
	}
}

func (h *Handler) isQueryRateLimited(query string, now time.Time) bool {
	h.mu.Lock()
	until, ok := h.rateLimited[query]
	h.mu.Unlock()
	return ok && now.Before(until)
}

func (h *Handler) setQueryRateLimit(query string, until time.Time) {
	h.mu.Lock()
	h.rateLimited[query] = until
	h.mu.Unlock()
}
