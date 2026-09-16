package ui

import (
	"fmt"
	"net/http"

	"rssam/internal/storage"
)

func parseCategoryFeedsPage(r *http.Request) (limit, offset int) {
	return limitOffset(r, sidebarLazyFeedThreshold, 200)
}

const feedsListPageSize = 50

func parseFeedsListPage(r *http.Request) (limit, offset, page int) {
	return pageOffset(r, feedsListPageSize)
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
	end := min(offset+limit, len(feeds))
	return feeds[offset:end]
}
