//go:build integration

package storage

import (
	"context"
	"testing"
)

// user_entries holds only exceptions (starred, removed): reading leaves no
// row behind, starring inserts one, unstarring deletes it, a removal is a
// row that survives unstar, and unsubscribing drops them all.
func TestIntegration_UserEntriesExceptionsOnly(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "exc-owner")
	reader := newIntegrationUser(t, store, "exc-reader")
	feed, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 4)
	if _, err := store.Subscribe(ctx, reader.ID, feed.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}
	rowsFor := func(userID int64) (n int) {
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM user_entries WHERE user_id = $1`, userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	unreadFor := func(userID int64) (n int) {
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM user_unread WHERE user_id = $1`, userID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if rowsFor(owner.ID) != 0 || rowsFor(reader.ID) != 0 || unreadFor(reader.ID) != 4 {
		t.Fatalf("after subscribe: rows owner=%d reader=%d unread=%d", rowsFor(owner.ID), rowsFor(reader.ID), unreadFor(reader.ID))
	}

	ids := []int64{entries[0].ID, entries[1].ID}
	read := EntryStatusRead
	if n, err := store.BulkUpdateEntries(ctx, reader.ID, ids, BulkEntryUpdate{Status: &read}); err != nil || n != 2 {
		t.Fatalf("mark read: n=%d err=%v", n, err)
	}
	if n, _ := store.BulkUpdateEntries(ctx, reader.ID, ids, BulkEntryUpdate{Status: &read}); n != 0 {
		t.Fatalf("repeated mark read must write nothing: %d", n)
	}
	if rowsFor(reader.ID) != 0 || unreadFor(reader.ID) != 2 {
		t.Fatalf("read leaves no exception row: rows=%d unread=%d", rowsFor(reader.ID), unreadFor(reader.ID))
	}
	got, err := store.GetEntry(ctx, reader.ID, entries[0].ID)
	if err != nil || got.Status != EntryStatusRead || got.Starred {
		t.Fatalf("read entry: %+v err=%v", got, err)
	}
	unread := EntryStatusUnread
	if n, err := store.BulkUpdateEntries(ctx, reader.ID, ids[:1], BulkEntryUpdate{Status: &unread}); err != nil || n != 1 {
		t.Fatalf("mark unread: n=%d err=%v", n, err)
	}
	if unreadFor(reader.ID) != 3 {
		t.Fatalf("unread back: %d", unreadFor(reader.ID))
	}

	star, unstar := true, false
	if n, err := store.BulkUpdateEntries(ctx, reader.ID, ids, BulkEntryUpdate{Starred: &star}); err != nil || n != 2 {
		t.Fatalf("star: n=%d err=%v", n, err)
	}
	if n, _ := store.BulkUpdateEntries(ctx, reader.ID, ids, BulkEntryUpdate{Starred: &star}); n != 0 {
		t.Fatalf("repeated star must write nothing: %d", n)
	}
	if rowsFor(reader.ID) != 2 {
		t.Fatalf("star rows: %d", rowsFor(reader.ID))
	}
	starred, _, err := store.ListEntries(ctx, reader.ID, ListEntriesFilter{Starred: &star, Limit: 10})
	if err != nil || len(starred) != 2 || !starred[0].Starred {
		t.Fatalf("starred list: %+v err=%v", starred, err)
	}
	if got, _ := store.GetEntry(ctx, reader.ID, entries[0].ID); !got.Starred || got.Status != EntryStatusUnread {
		t.Fatalf("starred unread entry: %+v", got)
	}
	// The star is the reader's alone.
	if got, _ := store.GetEntry(ctx, owner.ID, entries[0].ID); got.Starred {
		t.Fatal("owner must not see the reader's star")
	}
	if n, err := store.BulkUpdateEntries(ctx, reader.ID, ids[:1], BulkEntryUpdate{Starred: &unstar}); err != nil || n != 1 {
		t.Fatalf("unstar: n=%d err=%v", n, err)
	}
	if rowsFor(reader.ID) != 1 {
		t.Fatalf("unstar deletes the row: %d", rowsFor(reader.ID))
	}

	// Removal: a row per removed entry, unread gone, hidden from lists,
	// visible in the removed list; unstar on a removed starred row keeps
	// the removal.
	if n, err := store.MarkEntriesRemoved(ctx, reader.ID, []int64{entries[1].ID, entries[2].ID}); err != nil || n != 2 {
		t.Fatalf("remove: n=%d err=%v", n, err)
	}
	if n, _ := store.MarkEntriesRemoved(ctx, reader.ID, []int64{entries[2].ID}); n != 0 {
		t.Fatalf("repeated remove must write nothing: %d", n)
	}
	if rowsFor(reader.ID) != 2 || unreadFor(reader.ID) != 2 {
		t.Fatalf("after remove: rows=%d unread=%d", rowsFor(reader.ID), unreadFor(reader.ID))
	}
	if n, _ := store.BulkUpdateEntries(ctx, reader.ID, []int64{entries[1].ID}, BulkEntryUpdate{Starred: &unstar}); n != 0 || rowsFor(reader.ID) != 2 {
		t.Fatalf("unstar must not drop a removal: n=%d rows=%d", n, rowsFor(reader.ID))
	}
	if n, _ := store.BulkUpdateEntries(ctx, reader.ID, []int64{entries[2].ID}, BulkEntryUpdate{Status: &unread}); n != 0 {
		t.Fatalf("removed entry must not become unread: %d", n)
	}
	all, total, err := store.ListEntries(ctx, reader.ID, ListEntriesFilter{Limit: 10, WithTotal: true})
	if err != nil || total != 2 || len(all) != 2 {
		t.Fatalf("default list hides removed: total=%d n=%d err=%v", total, len(all), err)
	}
	removed := EntryStatusRemoved
	gone, total, err := store.ListEntries(ctx, reader.ID, ListEntriesFilter{Status: &removed, Limit: 10, WithTotal: true})
	if err != nil || total != 2 || len(gone) != 2 || gone[0].Status != EntryStatusRemoved {
		t.Fatalf("removed list: total=%d %+v err=%v", total, gone, err)
	}
	if got, _ := store.GetEntry(ctx, owner.ID, entries[2].ID); got.Status != EntryStatusUnread {
		t.Fatalf("owner still sees the entry: %+v", got)
	}
	if perFeed, _, err := store.UnreadCountsForUser(ctx, reader.ID); err != nil || perFeed[feed.ID] != 2 {
		t.Fatalf("unread count: %v err=%v", perFeed, err)
	}

	if err := store.Unsubscribe(ctx, reader.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if rowsFor(reader.ID) != 0 || unreadFor(reader.ID) != 0 {
		t.Fatalf("unsubscribe must drop exceptions and unread: rows=%d unread=%d", rowsFor(reader.ID), unreadFor(reader.ID))
	}
	if n, _ := store.BulkUpdateEntries(ctx, reader.ID, ids, BulkEntryUpdate{Starred: &star}); n != 0 {
		t.Fatalf("star without subscription: %d", n)
	}
	if rowsFor(owner.ID) != 0 {
		t.Fatalf("owner rows: %d", rowsFor(owner.ID))
	}
}
