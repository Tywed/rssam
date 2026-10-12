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

// Lists are index ranges, never sorts: user_entries.sort_at =
// COALESCE(published_at, created_at) frozen at insert, copied to
// user_unread, and the indexes of both tables order by it. The alias names
// the table that drives the list (ue, or uu for unread lists); sorting by
// the other one would hide the index from the planner.
func entryOrderClause(sort, alias string) string {
	if NormalizeEntrySort(sort) == EntrySortOldest {
		return alias + ".sort_at ASC, " + alias + ".entry_id ASC"
	}
	return alias + ".sort_at DESC, " + alias + ".entry_id DESC"
}
