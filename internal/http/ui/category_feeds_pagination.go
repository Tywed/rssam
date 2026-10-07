package ui

import (
	"net/http"
	"net/url"
	"strconv"

	"rssam/internal/storage"
)

func parseCategoryFeedsPage(r *http.Request) (limit, offset int) {
	return limitOffset(r, sidebarLazyFeedThreshold, 200)
}

const feedsListPageSize = 50

func parseFeedsListPage(r *http.Request) (limit, offset, page int) {
	return pageOffset(r, feedsListPageSize)
}

func feedsListPageLink(scope, filter string, page int) string {
	q := url.Values{}
	if scope == "mine" {
		q.Set("scope", scope)
	}
	if filter == "errors" || filter == "inactive" {
		q.Set("filter", filter)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return "/ui/feeds"
	}
	return "/ui/feeds?" + q.Encode()
}

func sliceFeedsPage(feeds []storage.Feed, limit, offset int) []storage.Feed {
	if offset >= len(feeds) {
		return nil
	}
	end := min(offset+limit, len(feeds))
	return feeds[offset:end]
}
