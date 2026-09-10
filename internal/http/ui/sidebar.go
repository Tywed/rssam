package ui

import (
	"net/http"
	"strconv"

	"rssam/internal/storage"
)

const sidebarLazyFeedThreshold = 50

type sidebarFeedsData struct {
	Feeds            []storage.Feed
	FeedUnreadCounts map[int64]int
	FeedID           int64
	EntrySort        string
	Limit            int
	Offset           int
	Total            int
}

func (h *Handler) handleSidebarCategoryFeeds(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	categoryID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || categoryID <= 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.renderSidebarCategoryFeeds(w, r, p.UserID, categoryID)
}

func (h *Handler) handleSidebarUncategorizedFeeds(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	h.renderSidebarCategoryFeeds(w, r, p.UserID, 0)
}

func (h *Handler) renderSidebarCategoryFeeds(w http.ResponseWriter, r *http.Request, userID, categoryID int64) {
	if h.cfg.Feeds == nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	limit, offset := parseCategoryFeedsPage(r)
	feeds, total, err := h.cfg.Feeds.ListFeedsByCategoryPaginated(r.Context(), userID, categoryID, limit, offset)
	if err != nil {
		http.Error(w, "list feeds failed", http.StatusInternalServerError)
		return
	}
	data := sidebarFeedsData{
		Feeds:     feeds,
		EntrySort: parseEntrySort(r),
		Limit:     limit,
		Offset:    offset,
		Total:     total,
	}
	if feedID := r.URL.Query().Get("feed_id"); feedID != "" {
		if id, err := strconv.ParseInt(feedID, 10, 64); err == nil && id > 0 {
			data.FeedID = id
		}
	}
	if h.cfg.Entries != nil {
		if snap, err := h.unreadCounts(r.Context(), userID); err == nil {
			data.FeedUnreadCounts = snap.feeds
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "sidebar_category_feeds", data); err != nil {
		h.log.ErrorContext(r.Context(), "sidebar feeds template failed", "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
