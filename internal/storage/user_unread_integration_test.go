//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func unreadRows(t *testing.T, store *PostgresStore, userID int64) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(context.Background(), `SELECT count(*) FROM user_unread WHERE user_id = $1`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func entryStatuses(t *testing.T, store *PostgresStore, userID int64, status *string) map[int64]string {
	t.Helper()
	list, _, err := store.ListEntries(context.Background(), userID, ListEntriesFilter{Status: status, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	out := map[int64]string{}
	for _, e := range list {
		out[e.ID] = e.Status
	}
	return out
}

// Unread is a row in user_unread; user_entries.status only ever says read
// or removed. Every status write keeps the two in step and the lists and
// counters report the combined view.
func TestIntegration_UserUnreadPresence(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "uu_alice")
	feed, entries := newIntegrationFeedWithEntries(t, store, alice.ID, 5)
	ids := make([]int64, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}

	if n := unreadRows(t, store, alice.ID); n != 5 {
		t.Fatalf("fan-out created %d unread rows, want 5", n)
	}
	var stored int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM user_entries WHERE user_id = $1 AND status = 'read'`, alice.ID).Scan(&stored)
	if stored != 5 {
		t.Fatalf("user_entries rows with status read: %d, want 5", stored)
	}
	if _, err := store.db.Exec(ctx, `UPDATE user_entries SET status = 'unread' WHERE user_id = $1 AND entry_id = $2`, alice.ID, ids[0]); err == nil {
		t.Fatal("user_entries must refuse the unread status")
	}

	read := EntryStatusRead
	unread := EntryStatusUnread
	n, err := store.BulkUpdateEntries(ctx, alice.ID, ids[:2], BulkEntryUpdate{Status: &read})
	if err != nil || n != 2 {
		t.Fatalf("mark read: n=%d err=%v", n, err)
	}
	if n, err := store.BulkUpdateEntries(ctx, alice.ID, ids[:2], BulkEntryUpdate{Status: &read}); err != nil || n != 0 {
		t.Fatalf("repeated mark read must change nothing: n=%d err=%v", n, err)
	}
	if n := unreadRows(t, store, alice.ID); n != 3 {
		t.Fatalf("unread rows after mark read: %d, want 3", n)
	}
	if got := entryStatuses(t, store, alice.ID, nil); got[ids[0]] != EntryStatusRead || got[ids[2]] != EntryStatusUnread || len(got) != 5 {
		t.Fatalf("combined list: %v", got)
	}
	if got := entryStatuses(t, store, alice.ID, &read); len(got) != 2 || got[ids[1]] != EntryStatusRead {
		t.Fatalf("read list: %v", got)
	}
	if got := entryStatuses(t, store, alice.ID, &unread); len(got) != 3 || got[ids[4]] != EntryStatusUnread {
		t.Fatalf("unread list: %v", got)
	}
	if c, _ := store.CountUnreadByFeed(ctx, alice.ID, feed.ID); c != 3 {
		t.Fatalf("unread by feed: %d", c)
	}

	// Mark unread again: one row back, the starred flag counted once with it.
	on := true
	n, err = store.BulkUpdateEntries(ctx, alice.ID, ids[:1], BulkEntryUpdate{Status: &unread, Starred: &on})
	if err != nil || n != 1 {
		t.Fatalf("mark unread + star: n=%d err=%v", n, err)
	}
	e, err := store.GetEntry(ctx, alice.ID, ids[0])
	if err != nil || e.Status != EntryStatusUnread || !e.Starred {
		t.Fatalf("entry after mark unread + star: %+v err=%v", e, err)
	}
	if n := unreadRows(t, store, alice.ID); n != 4 {
		t.Fatalf("unread rows after mark unread: %d, want 4", n)
	}

	// Single-entry update through the feed-scoped path.
	e, err = store.UpdateEntry(ctx, alice.ID, feed.ID, ids[0], UpdateEntryParams{Status: &read})
	if err != nil || e.Status != EntryStatusRead || !e.Starred {
		t.Fatalf("UpdateEntry read: %+v err=%v", e, err)
	}
	if _, err := store.UpdateEntry(ctx, alice.ID, feed.ID+1, ids[0], UpdateEntryParams{Status: &read}); err != ErrNotFound {
		t.Fatalf("UpdateEntry on a foreign feed: err=%v, want ErrNotFound", err)
	}

	// Removed takes the unread marker with it and stays out of every list.
	if n, err := store.MarkEntriesRemoved(ctx, alice.ID, ids[2:3]); err != nil || n != 1 {
		t.Fatalf("mark removed: n=%d err=%v", n, err)
	}
	if n := unreadRows(t, store, alice.ID); n != 2 {
		t.Fatalf("unread rows after remove: %d, want 2", n)
	}
	if got := entryStatuses(t, store, alice.ID, nil); len(got) != 4 {
		t.Fatalf("list after remove: %v", got)
	}
	if n, err := store.BulkUpdateEntries(ctx, alice.ID, ids[2:3], BulkEntryUpdate{Status: &unread}); err != nil || n != 0 {
		t.Fatalf("a removed entry must not become unread: n=%d err=%v", n, err)
	}

	if n, err := store.MarkAllFeedEntriesRead(ctx, alice.ID, feed.ID); err != nil || n != 2 {
		t.Fatalf("mark feed read: n=%d err=%v", n, err)
	}
	if c, _ := store.CountUnreadGlobalForUser(ctx, alice.ID); c != 0 {
		t.Fatalf("unread after mark feed read: %d", c)
	}

	// A user_entries row takes its marker along (FK cascade), so unsubscribe
	// and entry deletion need no extra statement.
	if _, err := store.BulkUpdateEntries(ctx, alice.ID, ids[3:5], BulkEntryUpdate{Status: &unread}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `DELETE FROM entries WHERE id = $1`, ids[3]); err != nil {
		t.Fatal(err)
	}
	if n := unreadRows(t, store, alice.ID); n != 1 {
		t.Fatalf("unread rows after entry delete: %d, want 1", n)
	}
}

// Subscribing backfills the feed's entries as read with only the newest
// SubscribeUnreadBackfill of them unread.
func TestIntegration_UserUnreadSubscribeBackfill(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "uu_owner")
	bob := newIntegrationUser(t, store, "uu_bob")
	feed, entries := newIntegrationFeedWithEntries(t, store, alice.ID, SubscribeUnreadBackfill+20)
	if _, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}
	if n := unreadRows(t, store, bob.ID); n != SubscribeUnreadBackfill {
		t.Fatalf("backfilled unread rows: %d, want %d", n, SubscribeUnreadBackfill)
	}
	var total int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM user_entries WHERE user_id = $1`, bob.ID).Scan(&total)
	if total != len(entries) {
		t.Fatalf("backfilled user_entries rows: %d, want %d", total, len(entries))
	}
	unread := EntryStatusUnread
	list, _, err := store.ListFeedEntries(ctx, bob.ID, feed.ID, ListEntriesFilter{Status: &unread, Limit: 200, Sort: EntrySortOldest})
	if err != nil || len(list) != SubscribeUnreadBackfill {
		t.Fatalf("unread list: %d err=%v", len(list), err)
	}
	// Entries are created with equal sort_at, so the newest are the highest ids.
	if list[0].ID != entries[20].ID {
		t.Fatalf("oldest unread is entry %d, want %d", list[0].ID, entries[20].ID)
	}
}

// A productive poll writes its entries and the feed's refresh metadata in
// one transaction.
func TestIntegration_CreateEntriesWithRefreshMeta(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "uu_meta")
	feed, _ := newIntegrationFeedWithEntries(t, store, alice.ID, 1)
	now := time.Now().UTC().Truncate(time.Second)
	next := now.Add(time.Hour)
	hash := "items-1"
	meta := UpdateFeedRefreshMetaParams{ID: feed.ID, ETag: "e1", LastCheckedAt: now, NextCheckAt: &next, ItemsHash: &hash}
	params := []CreateEntryParams{{Title: "n", URL: "https://example.com/n", Content: "b", Hash: fmt.Sprintf("h-%d", now.UnixNano())}}
	inserted, created, err := store.CreateEntriesWithRefreshMeta(ctx, feed.ID, params, meta)
	if err != nil || inserted != 1 || len(created) != 1 {
		t.Fatalf("inserted=%d created=%d err=%v", inserted, len(created), err)
	}
	after, err := store.GetFeedByID(ctx, feed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ETag != "e1" || after.ItemsHash != hash || after.LastEntryAt == nil || !after.LastEntryAt.Equal(now) || after.NextCheckAt == nil || !after.NextCheckAt.Equal(next) {
		t.Fatalf("meta after poll: etag=%q items=%q last_entry=%v next=%v", after.ETag, after.ItemsHash, after.LastEntryAt, after.NextCheckAt)
	}
	if n := unreadRows(t, store, alice.ID); n != 2 {
		t.Fatalf("unread rows: %d, want 2", n)
	}

	// The same items again: nothing inserted, last_entry_at untouched.
	later := now.Add(time.Minute)
	meta.LastCheckedAt = later
	inserted, _, err = store.CreateEntriesWithRefreshMeta(ctx, feed.ID, params, meta)
	if err != nil || inserted != 0 {
		t.Fatalf("repeat: inserted=%d err=%v", inserted, err)
	}
	after, _ = store.GetFeedByID(ctx, feed.ID)
	if !after.LastEntryAt.Equal(now) || after.LastCheckedAt == nil || !after.LastCheckedAt.Equal(later) {
		t.Fatalf("meta after repeat: last_entry=%v last_checked=%v", after.LastEntryAt, after.LastCheckedAt)
	}

	if _, _, err := store.CreateEntriesWithRefreshMeta(ctx, feed.ID+1000, params, UpdateFeedRefreshMetaParams{ID: feed.ID + 1000}); err != ErrNotFound {
		t.Fatalf("unknown feed: err=%v, want ErrNotFound", err)
	}
}
