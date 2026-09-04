package ui

import (
	"net/http"
)

const adminFeedsPageSize = 50

func parseAdminFeedsPage(r *http.Request) (limit, offset, page int) {
	limit = adminFeedsPageSize
	page = 1
	if v := r.URL.Query().Get("page"); v != "" {
		if n, err := strconvAtoi(v); err == nil && n > 0 {
			page = n
		}
	}
	offset = (page - 1) * limit
	return limit, offset, page
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
