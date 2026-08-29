package storage

import "testing"

func TestNormalizeEntrySort(t *testing.T) {
	if got := NormalizeEntrySort("oldest"); got != EntrySortOldest {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeEntrySort(""); got != EntrySortNewest {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeEntrySort("invalid"); got != EntrySortNewest {
		t.Fatalf("got %q", got)
	}
}

func TestEntryOrderClause(t *testing.T) {
	if entryOrderClause(EntrySortNewest) == entryOrderClause(EntrySortOldest) {
		t.Fatal("expected different order clauses")
	}
}
