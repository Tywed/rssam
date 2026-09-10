//go:build integration

package storage

import (
	"context"
	"strings"
	"testing"
)

// Lists page with LIMIT n+1 instead of count(*) OVER(): the total is only
// "there is a next page" unless WithTotal asks for the exact count. Either way
// the pages themselves must be identical.
func TestIntegration_EntryListPaging(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "paging_owner")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 7)
	if _, err := store.MarkEntriesRemoved(ctx, owner.ID, []int64{entries[6].ID}); err != nil {
		t.Fatal(err)
	}

	page1, total, err := store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Limit: 4})
	if err != nil || len(page1) != 4 || total != 5 {
		t.Fatalf("page1: len=%d total=%d err=%v", len(page1), total, err)
	}
	page2, total, err := store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Limit: 4, Offset: 4})
	if err != nil || len(page2) != 2 || total != 6 {
		t.Fatalf("page2: len=%d total=%d err=%v", len(page2), total, err)
	}
	exact1, total, err := store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Limit: 4, WithTotal: true})
	if err != nil || len(exact1) != 4 || total != 6 {
		t.Fatalf("exact page1: len=%d total=%d err=%v", len(exact1), total, err)
	}
	for i := range page1 {
		if page1[i].ID != exact1[i].ID {
			t.Fatalf("page differs at %d: %d vs %d", i, page1[i].ID, exact1[i].ID)
		}
	}
	_, total, err = store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Limit: 4, Offset: 4, WithTotal: true})
	if err != nil || total != 6 {
		t.Fatalf("exact page2: total=%d err=%v", total, err)
	}
	oldest, _, err := store.ListFeedEntries(ctx, owner.ID, feed.ID, ListEntriesFilter{Limit: 10, Sort: EntrySortOldest})
	if err != nil || len(oldest) != 6 || oldest[0].ID != page2[len(page2)-1].ID {
		t.Fatalf("oldest: len=%d first=%d want=%d err=%v", len(oldest), oldest[0].ID, page2[len(page2)-1].ID, err)
	}

	for _, withTotal := range []bool{false, true} {
		found, total, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: "entry", FeedID: &feed.ID, Limit: 4, WithTotal: withTotal})
		want := 5
		if withTotal {
			want = 6
		}
		if err != nil || len(found) != 4 || total != want {
			t.Fatalf("search withTotal=%v: len=%d total=%d err=%v", withTotal, len(found), total, err)
		}
	}
}

// The ORDER BY expression must match the indexed expression from migration
// 0034 or the planner sorts the whole user's set again.
func TestIntegration_EntryListUsesSortIndex(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "sortidx_owner")
	newIntegrationFeedWithEntries(t, store, owner.ID, 3)

	tx, err := store.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	unread := EntryStatusUnread
	for _, tc := range []struct {
		name  string
		where string
	}{
		{"status", "user_id = $1 AND status = $2"},
		{"all", "user_id = $1 AND status <> $2"},
	} {
		args := []any{owner.ID, unread}
		if tc.name == "all" {
			args[1] = EntryStatusRemoved
		}
		rows, err := tx.Query(ctx, "EXPLAIN SELECT id FROM entries WHERE "+tc.where+" ORDER BY "+entryOrderClause(EntrySortNewest)+" LIMIT 51", args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(line)
			plan.WriteByte('\n')
		}
		rows.Close()
		if !strings.Contains(plan.String(), "_sort_idx") || strings.Contains(plan.String(), "Sort Key") {
			t.Fatalf("%s: expected an ordered scan of a *_sort_idx index, got plan:\n%s", tc.name, plan.String())
		}
	}
}
