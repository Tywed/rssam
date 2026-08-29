package ui

import (
	"net/http"

	"rssam/internal/storage"
)

func parseEntrySort(r *http.Request) string {
	return storage.NormalizeEntrySort(r.URL.Query().Get("sort"))
}

func entrySortQueryValue(sort string) string {
	if storage.NormalizeEntrySort(sort) == storage.EntrySortOldest {
		return storage.EntrySortOldest
	}
	return ""
}

func appendEntrySortQuery(q map[string]string, sort string) {
	if v := entrySortQueryValue(sort); v != "" {
		q["sort"] = v
	}
}
