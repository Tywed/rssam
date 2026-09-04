package ui

import (
	"fmt"
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

const feedsListPageSize = 50

func parseFeedsListPage(r *http.Request) (limit, offset, page int) {
	limit = feedsListPageSize
	page = 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconvAtoi(v); err == nil && n > 0 {
			page = n
		}
	}
	offset = (page - 1) * limit
	return limit, offset, page
}

func feedsListPageLink(filter string, page int) string {
	if page < 1 {
		page = 1
	}
	if filter != "errors" && filter != "inactive" {
		if page <= 1 {
			return "/ui/feeds"
		}
		return fmt.Sprintf("/ui/feeds?page=%d", page)
	}
	if page <= 1 {
		return "/ui/feeds?filter=" + filter
	}
	return fmt.Sprintf("/ui/feeds?filter=%s&page=%d", filter, page)
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
