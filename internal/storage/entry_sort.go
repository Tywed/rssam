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

// entrySortExpr is user_entries.sort_at = COALESCE(published_at, created_at)
// frozen at insert; the user_entries_* indexes of migration 0050 order by
// it, so a page is an index range, never a sort of the whole set.
const entrySortExpr = "ue.sort_at"

func entryOrderClause(sort string) string {
	if NormalizeEntrySort(sort) == EntrySortOldest {
		return entrySortExpr + " ASC, ue.entry_id ASC"
	}
	return entrySortExpr + " DESC, ue.entry_id DESC"
}
