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

func TestTrimPage(t *testing.T) {
	mk := func(n int) []Entry { return make([]Entry, n) }
	if page, total := trimPage(0, 50, mk(10)); len(page) != 10 || total != 10 {
		t.Fatalf("short page: len=%d total=%d", len(page), total)
	}
	if page, total := trimPage(100, 50, mk(50)); len(page) != 50 || total != 150 {
		t.Fatalf("exact page: len=%d total=%d", len(page), total)
	}
	if page, total := trimPage(100, 50, mk(51)); len(page) != 50 || total != 151 {
		t.Fatalf("page with next: len=%d total=%d", len(page), total)
	}
	if page, total := trimPage(0, 50, nil); len(page) != 0 || total != 0 {
		t.Fatalf("empty: len=%d total=%d", len(page), total)
	}
}
