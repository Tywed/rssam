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

// entrySortExpr must stay textually identical to the expression indexed by
// migration 0034, otherwise the planner falls back to sorting the whole set.
const entrySortExpr = "COALESCE(published_at, created_at)"

func entryOrderClause(sort string) string {
	if NormalizeEntrySort(sort) == EntrySortOldest {
		return entrySortExpr + " ASC NULLS FIRST, id ASC"
	}
	return entrySortExpr + " DESC NULLS LAST, id DESC"
}
