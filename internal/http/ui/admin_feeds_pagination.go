package ui

import (
	"net/http"
)

const adminFeedsPageSize = 50

func parseAdminFeedsPage(r *http.Request) (limit, offset, page int) {
	return pageOffset(r, adminFeedsPageSize)
}

func adminFeedsPageCount(total, limit int) int {
	if total <= 0 {
		return 1
	}
	if limit <= 0 {
		limit = adminFeedsPageSize
	}
	return (total + limit - 1) / limit
}

func adminFeedsPageLink(status, sortKey, order string, page int) string {
	return adminFeedsListURL(status, sortKey, order, page)
}
