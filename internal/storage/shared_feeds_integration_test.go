//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"rssam/internal/migrations"
)

// One catalog row per URL: the second user who adds the same URL gets
// ErrDuplicateFeedURL and subscribes instead; a subscription backfills the
// newest SubscribeUnreadBackfill entries unread, the rest read; new entries
// fan out to every subscriber; per-user state never leaks.
func TestIntegration_SharedFeed_SubscribeBackfillAndFanOut(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "sf_alice")
	bob := newIntegrationUser(t, store, "sf_bob")
	bobCat, _ := store.CreateCategory(ctx, bob.ID, "Bob", "")

	feed, entries := newIntegrationFeedWithEntries(t, store, alice.ID, SubscribeUnreadBackfill+20)
	if _, err := store.CreateFeed(ctx, bob.ID, CreateFeedParams{FeedURL: feed.FeedURL, FeedType: "rss", IntervalMinutes: 60}); !errors.Is(err, ErrDuplicateFeedURL) {
		t.Fatalf("second catalog row for the same URL: err=%v", err)
	}
	if _, err := store.GetFeed(ctx, bob.ID, feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsubscribed user sees the feed: %v", err)
	}
	if _, err := store.BulkUpdateEntries(ctx, alice.ID, []int64{entries[0].ID}, BulkEntryUpdate{Status: ptr(EntryStatusRead)}); err != nil {
		t.Fatal(err)
	}

	sub, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{CategoryID: &bobCat.ID})
	if err != nil || sub.UserID != bob.ID || sub.CategoryID == nil || *sub.CategoryID != bobCat.ID {
		t.Fatalf("subscribe: %+v err=%v", sub, err)
	}
	if _, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{}); !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("double subscribe: %v", err)
	}
	if n, _ := store.CountUnreadByFeed(ctx, bob.ID, feed.ID); n != SubscribeUnreadBackfill {
		t.Fatalf("backfill unread for bob = %d, want %d", n, SubscribeUnreadBackfill)
	}
	if n, _ := store.CountUnreadByFeed(ctx, alice.ID, feed.ID); n != SubscribeUnreadBackfill+19 {
		t.Fatalf("alice unread changed by bob's subscription: %d", n)
	}
	if n, _ := store.CountUnreadByCategory(ctx, bob.ID, bobCat.ID); n != SubscribeUnreadBackfill {
		t.Fatalf("bob category unread = %d", n)
	}
	got, err := store.GetFeed(ctx, bob.ID, feed.ID)
	if err != nil || got.CategoryID == nil || *got.CategoryID != bobCat.ID || got.OwnerID != alice.ID {
		t.Fatalf("bob's view of the feed: %+v err=%v", got, err)
	}
	if aliceView, _ := store.GetFeed(ctx, alice.ID, feed.ID); aliceView.CategoryID != nil {
		t.Fatalf("bob's category leaked into alice's view: %+v", aliceView)
	}
	subs, err := store.ListFeedSubscribers(ctx, feed.ID)
	if err != nil || len(subs) != 2 {
		t.Fatalf("subscribers: %v err=%v", subs, err)
	}

	// Fan-out on insert: one entries row, one user_entries row per subscriber.
	n, created, err := store.CreateEntries(ctx, feed.ID, []CreateEntryParams{{Title: "fresh", URL: "https://example.com/sf/fresh", Hash: "sf-fresh"}})
	if err != nil || n != 1 || len(created) != 1 || created[0].Status != EntryStatusUnread {
		t.Fatalf("create: n=%d created=%v err=%v", n, created, err)
	}
	for _, u := range []User{alice, bob} {
		e, err := store.GetEntry(ctx, u.ID, created[0].ID)
		if err != nil || e.Status != EntryStatusUnread {
			t.Fatalf("user %d fresh entry: %+v err=%v", u.ID, e, err)
		}
	}
	if _, err := store.UpdateEntry(ctx, bob.ID, feed.ID, created[0].ID, UpdateEntryParams{Starred: ptr(true), Status: ptr(EntryStatusRead)}); err != nil {
		t.Fatal(err)
	}
	if e, _ := store.GetEntry(ctx, alice.ID, created[0].ID); e.Starred || e.Status != EntryStatusUnread {
		t.Fatalf("bob's star/read leaked to alice: %+v", e)
	}
	if _, err := store.MarkAllFeedEntriesRead(ctx, bob.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.CountUnreadByFeed(ctx, alice.ID, feed.ID); n != SubscribeUnreadBackfill+20 {
		t.Fatalf("bob's mark-all leaked: alice unread=%d", n)
	}
	list, total, err := store.ListEntries(ctx, bob.ID, ListEntriesFilter{Starred: ptr(true), WithTotal: true})
	if err != nil || total != 1 || len(list) != 1 || list[0].ID != created[0].ID {
		t.Fatalf("bob starred list: %v total=%d err=%v", list, total, err)
	}

	// Unsubscribe keeps the catalog row and alice's state; bob's rows go.
	if err := store.DeleteFeed(ctx, bob.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, feed.ID); err != nil {
		t.Fatalf("catalog row vanished with one unsubscribe: %v", err)
	}
	var bobRows int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM user_entries WHERE user_id = $1`, bob.ID).Scan(&bobRows)
	if bobRows != 0 {
		t.Fatalf("bob still has %d user_entries", bobRows)
	}
	if e, err := store.GetEntry(ctx, alice.ID, created[0].ID); err != nil || e.Status != EntryStatusUnread {
		t.Fatalf("alice lost her entry: %+v err=%v", e, err)
	}
	// Last subscriber leaving removes the catalog row and its entries.
	if err := store.DeleteFeed(ctx, alice.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("catalog row survives with no subscribers: %v", err)
	}
	var left int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE feed_id = $1`, feed.ID).Scan(&left)
	if left != 0 {
		t.Fatalf("%d entries survive the catalog row", left)
	}
}

// A subscriber deleting an entry hides it for that user only; retention
// purges per-user removed rows and drops the entries row once no reader is
// left; a stripped (removed_at) entry goes for everyone.
func TestIntegration_SharedFeed_RemovalAndRetention(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "sfr_alice")
	bob := newIntegrationUser(t, store, "sfr_bob")
	feed, entries := newIntegrationFeedWithEntries(t, store, alice.ID, 3)
	if _, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.MarkEntriesRemoved(ctx, bob.ID, []int64{entries[0].ID, entries[1].ID}); err != nil {
		t.Fatal(err)
	}
	if e, err := store.GetEntry(ctx, alice.ID, entries[0].ID); err != nil || e.Status != EntryStatusUnread {
		t.Fatalf("bob's removal leaked to alice: %+v err=%v", e, err)
	}
	if _, err := store.MarkEntriesRemoved(ctx, alice.ID, []int64{entries[1].ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.StripEntryPayloadAfterWebhook(ctx, entries[2].ID); err != nil {
		t.Fatal(err)
	}
	for _, u := range []User{alice, bob} {
		if e, _ := store.GetEntry(ctx, u.ID, entries[2].ID); e.Status != EntryStatusRemoved {
			t.Fatalf("strip did not remove for user %d: %+v", u.ID, e)
		}
	}
	if _, err := store.db.Exec(ctx, `UPDATE user_entries SET updated_at = now() - interval '40 days' WHERE feed_id = $1`, feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE entries SET removed_at = now() - interval '40 days' WHERE id = $1`, entries[2].ID); err != nil {
		t.Fatal(err)
	}
	res, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{RemovedEntriesBefore: RetentionCutoff(time.Now(), 30), WebhookLogsBefore: RetentionCutoff(time.Now(), 90)})
	if err != nil {
		t.Fatal(err)
	}
	// entries[1] (removed by both) and entries[2] (stripped) are gone,
	// entries[0] stays for alice.
	if res.RemovedEntries != 2 {
		t.Fatalf("removed entries = %d, want 2", res.RemovedEntries)
	}
	if _, err := store.GetEntry(ctx, alice.ID, entries[0].ID); err != nil {
		t.Fatalf("alice's live entry purged: %v", err)
	}
	var rows int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE feed_id = $1`, feed.ID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("entries left = %d, want 1", rows)
	}
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM user_entries WHERE entry_id = $1`, entries[0].ID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("user_entries for the surviving entry = %d, want 1 (alice)", rows)
	}
	// The poll dedup still knows the stripped item.
	if known, _ := store.FilterKnownEntryHashes(ctx, feed.ID, []string{entries[2].Hash}); len(known) != 1 {
		t.Fatal("stripped hash lost")
	}
}

// Category poll hours and bulk category updates work through
// subscriptions; the catalog interval is shared, the webhook is per user.
func TestIntegration_SharedFeed_CategoryBulkAndQuota(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "sfc_alice")
	bob := newIntegrationUser(t, store, "sfc_bob")
	cat, _ := store.CreateCategory(ctx, alice.ID, "A", "")
	feed := newFeedForUser(t, store, alice.ID, "sfc", 60)
	if _, err := store.UpdateFeed(ctx, alice.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, IntervalMinutes: 60, CategoryID: &cat.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}
	wh, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: alice.ID, Name: "w", URL: "https://example.com/w", Headers: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ids, n, err := store.BulkUpdateFeedsByCategory(ctx, alice.ID, cat.ID, BulkFeedUpdate{IntervalMinutes: ptr(15)})
	if err != nil || n != 1 || len(ids) != 1 {
		t.Fatalf("bulk interval: ids=%v n=%d err=%v", ids, n, err)
	}
	if _, n, err := store.BulkUpdateFeedsByCategory(ctx, alice.ID, cat.ID, BulkFeedUpdate{WebhookSet: true, WebhookID: &wh.ID}); err != nil || n != 1 {
		t.Fatalf("bulk webhook: n=%d err=%v", n, err)
	}
	if _, n, err := store.BulkUpdateFeedsByCategory(ctx, bob.ID, cat.ID, BulkFeedUpdate{IntervalMinutes: ptr(5)}); !errors.Is(err, ErrNotFound) || n != 0 {
		t.Fatalf("bulk on another user's category: n=%d err=%v", n, err)
	}
	aliceView, _ := store.GetFeed(ctx, alice.ID, feed.ID)
	bobView, _ := store.GetFeed(ctx, bob.ID, feed.ID)
	if aliceView.IntervalMinutes != 15 || bobView.IntervalMinutes != 15 {
		t.Fatalf("interval is catalog-wide: alice=%d bob=%d", aliceView.IntervalMinutes, bobView.IntervalMinutes)
	}
	if aliceView.WebhookID == nil || *aliceView.WebhookID != wh.ID || bobView.WebhookID != nil {
		t.Fatalf("webhook must be per subscription: alice=%v bob=%v", aliceView.WebhookID, bobView.WebhookID)
	}
	if got, _ := store.CountOwnedFeeds(ctx, alice.ID); got != 1 {
		t.Fatalf("alice owns %d feeds, want 1", got)
	}
	if got, _ := store.CountOwnedFeeds(ctx, bob.ID); got != 0 {
		t.Fatalf("bob owns %d feeds, want 0 (subscriber only)", got)
	}
	// Deleting the owner keeps the feed for bob, owner_id becomes 0.
	if err := store.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetFeedByID(ctx, feed.ID)
	if err != nil || after.OwnerID != 0 {
		t.Fatalf("feed after owner deletion: %+v err=%v", after, err)
	}
	// And deleting the last subscriber removes the orphan catalog row.
	if err := store.DeleteUser(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("orphan catalog row left after the last subscriber: %v", err)
	}
}

// 0050 replayed on a pre-0050 shape: two users with the same URL end up on
// one catalog row (the lower owner id keeps it), equal-hash entries collapse
// with each user's state preserved, the other entries move over, and a
// second run is a no-op.
func TestIntegration_Migration0050_SharedFeedsMerge(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	for _, q := range []string{
		`DROP TABLE user_entries`,
		`DROP TABLE subscriptions`,
		`DROP INDEX feeds_feed_url_uidx`,
		`ALTER TABLE feeds DROP COLUMN owner_id, ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1 REFERENCES users(id) ON DELETE CASCADE,
		   ADD COLUMN category_id BIGINT REFERENCES categories(id) ON DELETE SET NULL, ADD COLUMN webhook_id BIGINT REFERENCES webhooks(id) ON DELETE SET NULL`,
		`ALTER TABLE entries DROP COLUMN removed_at, ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1, ADD COLUMN status TEXT NOT NULL DEFAULT 'unread', ADD COLUMN starred BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE enclosures ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1`,
		`INSERT INTO users(id, username, password_hash, role) VALUES (11, 'm50_a', 'h', 'admin'), (12, 'm50_b', 'h', 'reader')`,
		`INSERT INTO categories(id, user_id, title) VALUES (1, 12, 'B')`,
		`INSERT INTO feeds(id, user_id, feed_url, feed_type, title, interval_minutes, category_id) VALUES
		   (1, 11, 'https://x/a.xml', 'rss', 'A', 60, NULL), (2, 12, 'https://x/a.xml', 'rss', 'A2', 30, 1), (3, 12, 'https://x/b.xml', 'rss', 'B', 60, 1)`,
		`INSERT INTO entries(id, user_id, feed_id, title, url, content, hash, status, starred, search_vector) VALUES
		   (1, 11, 1, 'e1', 'https://x/1', 'c', 'h1', 'read', FALSE, ''),
		   (2, 12, 2, 'e1', 'https://x/1', 'c', 'h1', 'unread', TRUE, ''),
		   (3, 12, 2, 'e2', 'https://x/2', 'c', 'h2', 'unread', FALSE, ''),
		   (4, 12, 3, 'b1', 'https://x/b1', 'c', 'hb1', 'unread', FALSE, ''),
		   (5, 11, 1, '', 'https://x/s', '', 'hs', 'removed', FALSE, '')`,
		`INSERT INTO enclosures(user_id, entry_id, url) VALUES (12, 2, 'https://x/enc')`,
		`INSERT INTO feed_entry_dedup(feed_id, hash, url) VALUES (2, 'hd', 'https://x/d')`,
	} {
		if _, err := store.db.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	plan, err := migrations.SharedFeedsPlan(ctx, store.db)
	if err != nil || len(plan) != 1 || plan[0].KeepID != 1 || len(plan[0].LoseIDs) != 1 || plan[0].LoseIDs[0] != 2 || plan[0].LoseEntries != 2 || plan[0].SameEntries != 1 {
		t.Fatalf("plan: %+v err=%v", plan, err)
	}
	body, err := migrations.Source("0050_shared_feeds.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, body); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	if plan, _ := migrations.SharedFeedsPlan(ctx, store.db); plan != nil {
		t.Fatalf("plan after migration: %+v", plan)
	}

	feeds, _ := store.ListAllFeeds(ctx, 10)
	if len(feeds) != 2 || feeds[0].ID != 1 || feeds[0].OwnerID != 11 || feeds[1].ID != 3 {
		t.Fatalf("feeds after merge: %+v", feeds)
	}
	subs, _ := store.ListFeedSubscribers(ctx, 1)
	if len(subs) != 2 || subs[0].UserID != 11 || subs[1].UserID != 12 || subs[1].CategoryID == nil || *subs[1].CategoryID != 1 {
		t.Fatalf("subscriptions of the merged feed: %+v", subs)
	}
	type st struct {
		status  string
		starred bool
	}
	state := func(userID, entryID int64) st {
		var s st
		_ = store.db.QueryRow(ctx, `SELECT status, starred FROM user_entries WHERE user_id = $1 AND entry_id = $2`, userID, entryID).Scan(&s.status, &s.starred)
		return s
	}
	if got := state(11, 1); got != (st{"read", false}) {
		t.Fatalf("a's state on the shared entry: %+v", got)
	}
	if got := state(12, 1); got != (st{"unread", true}) {
		t.Fatalf("b's state remapped onto the keeper entry: %+v", got)
	}
	if got := state(12, 3); got != (st{"unread", false}) {
		t.Fatalf("b's moved entry: %+v", got)
	}
	var n int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE id = 2`).Scan(&n)
	if n != 0 {
		t.Fatal("duplicate entry survived")
	}
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE id = 3 AND feed_id = 1`).Scan(&n)
	if n != 1 {
		t.Fatal("entry 3 not moved to the keeper feed")
	}
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM enclosures WHERE entry_id = 1 AND url = 'https://x/enc'`).Scan(&n)
	if n != 1 {
		t.Fatal("enclosure of the collapsed entry not re-pointed")
	}
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM feed_entry_dedup WHERE feed_id = 1 AND hash = 'hd'`).Scan(&n)
	if n != 1 {
		t.Fatal("dedup hash of the merged feed not moved")
	}
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE id = 5 AND removed_at IS NOT NULL`).Scan(&n)
	if n != 1 {
		t.Fatal("stripped entry did not get removed_at")
	}
	for _, col := range []string{"feeds.user_id", "feeds.category_id", "feeds.webhook_id", "entries.user_id", "entries.status", "entries.starred", "enclosures.user_id"} {
		var tbl, c string
		fmt.Sscanf(col, "%[^.].%s", &tbl, &c)
		_ = store.db.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, tbl, c).Scan(&n)
		if n != 0 {
			t.Fatalf("%s still present", col)
		}
	}
}
