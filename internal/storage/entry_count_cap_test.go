package storage

import (
	"strings"
	"testing"
)

// Every list total is counted through its own list/candidate query and
// stops at ListTotalCap; the former per-status count queries (full join
// for unread, per-feed arithmetic for all/read) grew with the list.
func TestEntryListScopeCountIsCapped(t *testing.T) {
	starred := true
	for _, tc := range []struct {
		name   string
		status *string
		conds  entryListConds
	}{
		{"all", nil, entryListConds{}},
		{"read", strPtr(EntryStatusRead), entryListConds{}},
		{"unread", strPtr(EntryStatusUnread), entryListConds{}},
		{"removed", strPtr(EntryStatusRemoved), entryListConds{}},
		{"starred", nil, entryListConds{Starred: &starred}},
	} {
		scope, err := entryListScope(1, tc.status, tc.conds)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !strings.HasPrefix(scope.count, "SELECT count(*) FROM (SELECT 1 ") || !strings.Contains(scope.count, "LIMIT 10001) c") {
			t.Fatalf("%s: count query not capped:\n%s", tc.name, scope.count)
		}
		body := scope.list
		if scope.cand != "" {
			body = scope.cand
		}
		if !strings.Contains(scope.count, body) {
			t.Fatalf("%s: count does not reuse the list query", tc.name)
		}
	}
}

func strPtr(s string) *string { return &s }
