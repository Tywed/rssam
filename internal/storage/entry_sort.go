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

// Lists are index ranges, never sorts: entries.sort_at =
// COALESCE(published_at, created_at) frozen at insert, copied to
// user_unread and user_entries, and the indexes of all three order by it.
// The alias names the table that drives the list (e, uu or ue); sorting by
// another one would hide the index from the planner.
func entryOrderClause(sort, alias string) string {
	id := alias + ".entry_id"
	if alias == "e" || alias == "cand" {
		id = alias + ".id"
	}
	if NormalizeEntrySort(sort) == EntrySortOldest {
		return alias + ".sort_at ASC, " + id + " ASC"
	}
	return alias + ".sort_at DESC, " + id + " DESC"
}
