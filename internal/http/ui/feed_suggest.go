package ui

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"rssam/internal/storage"
)

type feedSuggestDTO struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	FeedURL       string `json:"feed_url"`
	CategoryID    *int64 `json:"category_id,omitempty"`
	CategoryTitle string `json:"category_title,omitempty"`
}

func (h *Handler) handleFeedSuggest(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.cfg.Feeds == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	filter := storage.SearchFeedsFilter{
		Query: q,
		Limit: storage.DefaultFeedSuggestLimit,
	}
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("category_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid category_id"})
			return
		}
		filter.CategoryID = &id
	}

	hasQuery := utf8.RuneCountInString(q) >= storage.MinFeedSearchQueryRunes
	if !hasQuery && filter.CategoryID == nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []feedSuggestDTO{}})
		return
	}

	feeds, err := h.cfg.Feeds.SearchFeeds(r.Context(), p.UserID, filter)
	if err != nil {
		if h.log != nil {
			h.log.Error("feed suggest failed", "err", err)
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "search failed"})
		return
	}

	catTitles := map[int64]string{}
	if h.cfg.Categories != nil {
		if cats, _, err := h.cfg.Categories.ListCategories(r.Context(), p.UserID, storage.NoLimit, 0); err == nil {
			for _, c := range cats {
				catTitles[c.ID] = c.Title
			}
		}
	}

	out := make([]feedSuggestDTO, 0, len(feeds))
	for _, f := range feeds {
		item := feedSuggestDTO{ID: f.ID, Title: f.Title, FeedURL: f.FeedURL, CategoryID: f.CategoryID}
		if f.CategoryID != nil {
			item.CategoryTitle = catTitles[*f.CategoryID]
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}
