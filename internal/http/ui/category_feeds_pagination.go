package ui

import (
	"net/http"

	"rssam/internal/storage"
)

func parseCategoryFeedsPage(r *http.Request) (limit, offset int) {
	limit = sidebarLazyFeedThreshold
	offset = 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconvAtoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconvAtoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func sliceFeedsPage(feeds []storage.Feed, limit, offset int) []storage.Feed {
	if offset >= len(feeds) {
		return nil
	}
	end := offset + limit
	if end > len(feeds) {
		end = len(feeds)
	}
	return feeds[offset:end]
}
