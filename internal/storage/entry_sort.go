package storage

import "strings"

const (
	EntrySortNewest = "newest"
	EntrySortOldest = "oldest"
)

// NormalizeEntrySort returns a supported entry sort order.
func NormalizeEntrySort(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case EntrySortOldest:
		return EntrySortOldest
	default:
		return EntrySortNewest
	}
}

func entryOrderClause(sort string) string {
	if NormalizeEntrySort(sort) == EntrySortOldest {
		return "COALESCE(published_at, created_at) ASC NULLS FIRST, id ASC"
	}
	return "COALESCE(published_at, created_at) DESC NULLS LAST, id DESC"
}
