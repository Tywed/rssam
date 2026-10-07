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
	cat, _ := store.CreateCategory(ctx, "Shared", "")

	feed, entries := newIntegrationFeedWithEntries(t, store, alice.ID, SubscribeUnreadBackfill+20)
	if _, err := store.UpdateFeed(ctx, alice.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, IntervalMinutes: 60, CategoryID: &cat.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFeed(ctx, bob.ID, CreateFeedParams{FeedURL: feed.FeedURL, FeedType: "rss", IntervalMinutes: 60}); !errors.Is(err, ErrDuplicateFeedURL) {
		t.Fatalf("second catalog row for the same URL: err=%v", err)
	}
	if _, err := store.GetFeed(ctx, bob.ID, feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unsubscribed user sees the feed: %v", err)
	}
	if _, err := store.BulkUpdateEntries(ctx, alice.ID, []int64{entries[0].ID}, BulkEntryUpdate{Status: ptr(EntryStatusRead)}); err != nil {
		t.Fatal(err)
	}

	sub, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{})
	if err != nil || sub.UserID != bob.ID || sub.FeedID != feed.ID {
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
	if n, _ := store.CountUnreadByCategory(ctx, bob.ID, cat.ID); n != SubscribeUnreadBackfill {
		t.Fatalf("bob category unread = %d", n)
	}
	got, err := store.GetFeed(ctx, bob.ID, feed.ID)
	if err != nil || got.CategoryID == nil || *got.CategoryID != cat.ID || got.OwnerID != alice.ID || !got.Subscribed {
		t.Fatalf("bob's view of the feed: %+v err=%v", got, err)
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
	if err := store.Unsubscribe(ctx, bob.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Unsubscribe(ctx, bob.ID, feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second unsubscribe: %v", err)
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
	// The last subscriber leaving keeps the catalog row and its entries;
	// the feed simply stops being polled.
	if err := store.Unsubscribe(ctx, alice.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, feed.ID); err != nil {
		t.Fatalf("catalog row with no subscribers: %v", err)
	}
	var left int
	_ = store.db.QueryRow(ctx, `SELECT count(*) FROM entries WHERE feed_id = $1`, feed.ID).Scan(&left)
	if left == 0 {
		t.Fatal("entries removed with the last subscription")
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
	cat, _ := store.CreateCategory(ctx, "A", "")
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
	if _, n, err := store.BulkUpdateFeedsByCategory(ctx, bob.ID, cat.ID+1000, BulkFeedUpdate{IntervalMinutes: ptr(5)}); !errors.Is(err, ErrNotFound) || n != 0 {
		t.Fatalf("bulk on a missing category: n=%d err=%v", n, err)
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
	// Deleting the last subscriber keeps the catalog row: feeds belong to
	// the catalog, not to their readers.
	if err := store.DeleteUser(ctx, bob.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, feed.ID); err != nil {
		t.Fatalf("catalog row after the last subscriber left: %v", err)
	}
	if subs, _ := store.ListFeedSubscribers(ctx, feed.ID); len(subs) != 0 {
		t.Fatalf("subscriptions left: %+v", subs)
	}
}

// 0050 replayed on a pre-0050 shape (feeds.category_id is per-owner there):
// two users with the same URL end up on
// one catalog row (the lower owner id keeps it), equal-hash entries collapse
// with each user's state preserved, the other entries move over, and a
// second run is a no-op.
func TestIntegration_Migration0050_SharedFeedsMerge(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	for _, q := range []string{
		`DROP TABLE user_entries`,
		`DROP TABLE subscriptions`,
		`DROP TABLE category_followers`,
		`DROP INDEX feeds_feed_url_uidx`,
		`ALTER TABLE categories ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1`,
		`ALTER TABLE feeds DROP COLUMN owner_id, ADD COLUMN user_id BIGINT NOT NULL DEFAULT 1 REFERENCES users(id) ON DELETE CASCADE,
		   ADD COLUMN webhook_id BIGINT REFERENCES webhooks(id) ON DELETE SET NULL`,
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
	// 0054 on top (twice): the merged feed takes the earliest subscriber's
	// category since its owner had none; user 12 read every feed of the
	// category and becomes its follower, user 11 does not.
	body54, err := migrations.Source("0054_shared_categories.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, body54); err != nil {
			t.Fatalf("0054 run %d: %v", run, err)
		}
	}

	feeds, err := store.ListAllFeeds(ctx, 10)
	if err != nil || len(feeds) != 2 || feeds[0].ID != 1 || feeds[0].OwnerID != 11 || feeds[1].ID != 3 {
		t.Fatalf("feeds after merge: %+v err=%v", feeds, err)
	}
	if feeds[0].CategoryID == nil || *feeds[0].CategoryID != 1 || feeds[1].CategoryID == nil || *feeds[1].CategoryID != 1 {
		t.Fatalf("feed categories after 0054: %v %v", feeds[0].CategoryID, feeds[1].CategoryID)
	}
	if followed, _ := store.ListFollowedCategories(ctx, 12); !followed[1] {
		t.Fatalf("user 12 must follow category 1: %v", followed)
	}
	if followed, _ := store.ListFollowedCategories(ctx, 11); followed[1] {
		t.Fatalf("user 11 must not follow category 1: %v", followed)
	}
	subs, _ := store.ListFeedSubscribers(ctx, 1)
	if len(subs) != 2 || subs[0].UserID != 11 || subs[1].UserID != 12 {
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

// The catalog: SubscribeByURL finds the row by URL, CatalogFeeds shows every
// feed with Subscribed per user, DeleteFeed by a subscriber removes it for
// everyone, and a subscription cannot point at another user's webhook.
func TestIntegration_SharedFeed_CatalogAndDelete(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "sfc_alice")
	bob := newIntegrationUser(t, store, "sfc_bob")
	cat, _ := store.CreateCategory(ctx, "Cat", "")
	aliceHook, err := store.CreateWebhook(ctx, CreateWebhookParams{UserID: alice.ID, Name: "a", Kind: WebhookKindHTTP, URL: "https://example.com/hook", Headers: []byte(`{}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	feed, _ := newIntegrationFeedWithEntries(t, store, alice.ID, 3)
	other, _ := newIntegrationFeedWithEntries(t, store, alice.ID, 1)
	if _, err := store.UpdateFeed(ctx, alice.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, IntervalMinutes: 60, CategoryID: &cat.ID}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{WebhookID: &aliceHook.ID}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("subscribe with alice's webhook: %v", err)
	}
	if _, err := store.CreateFeed(ctx, bob.ID, CreateFeedParams{FeedURL: fmt.Sprintf("https://example.com/sfc/%d", time.Now().UnixNano()), FeedType: "rss", IntervalMinutes: 60, WebhookID: &aliceHook.ID}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("create feed with alice's webhook: %v", err)
	}
	if _, err := store.UpdateFeed(ctx, alice.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, IntervalMinutes: 60, CategoryID: ptr(cat.ID + 1000)}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("update feed with a missing category: %v", err)
	}

	got, err := store.SubscribeByURL(ctx, bob.ID, feed.FeedURL, SubscriptionParams{})
	if err != nil || got.ID != feed.ID || got.CategoryID == nil || *got.CategoryID != cat.ID || !got.Subscribed {
		t.Fatalf("subscribe by url: %+v err=%v", got, err)
	}
	if _, err := store.SubscribeByURL(ctx, bob.ID, feed.FeedURL, SubscriptionParams{}); !errors.Is(err, ErrAlreadySubscribed) {
		t.Fatalf("subscribe by url twice: %v", err)
	}
	if _, err := store.SubscribeByURL(ctx, bob.ID, "https://example.com/sfc/none", SubscriptionParams{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("subscribe by unknown url: %v", err)
	}
	if _, err := store.UpdateSubscription(ctx, bob.ID, feed.ID, SubscriptionParams{WebhookID: &aliceHook.ID}); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("update subscription with alice's webhook: %v", err)
	}

	list, total, err := store.CatalogFeeds(ctx, bob.ID, nil, 50, 0)
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("catalog: total=%d len=%d err=%v", total, len(list), err)
	}
	byID := map[int64]Feed{}
	for _, c := range list {
		byID[c.ID] = c
	}
	if c := byID[feed.ID]; !c.Subscribed || c.OwnerID != alice.ID || c.WebhookID != nil {
		t.Fatalf("catalog row for shared feed: %+v", c)
	}
	if c := byID[other.ID]; c.Subscribed {
		t.Fatalf("catalog row for alice-only feed: %+v", c)
	}
	inCat, total, err := store.CatalogFeeds(ctx, bob.ID, &cat.ID, 50, 0)
	if err != nil || total != 1 || len(inCat) != 1 || inCat[0].ID != feed.ID {
		t.Fatalf("catalog by category: %v total=%d err=%v", inCat, total, err)
	}
	uncat, total, err := store.CatalogFeeds(ctx, bob.ID, ptr(int64(0)), 50, 0)
	if err != nil || total != 1 || len(uncat) != 1 || uncat[0].ID != other.ID {
		t.Fatalf("catalog uncategorized: %v total=%d err=%v", uncat, total, err)
	}
	counts, err := store.CatalogCountsByCategory(ctx)
	if err != nil || counts.ByCategory[cat.ID] != 1 || counts.Uncategorized != 1 || counts.Total != 2 {
		t.Fatalf("catalog counts: %+v err=%v", counts, err)
	}
	subCounts, err := store.FeedSubscriberCounts(ctx)
	if err != nil || subCounts[feed.ID] != 2 || subCounts[other.ID] != 1 {
		t.Fatalf("subscriber counts: %v err=%v", subCounts, err)
	}
	names, err := store.ListFeedSubscriberNames(ctx, feed.ID)
	if err != nil || len(names) != 2 || names[0].Username != alice.Username || names[1].Username != bob.Username {
		t.Fatalf("subscriber names: %+v err=%v", names, err)
	}

	// Bob (a subscriber, not the owner) deletes the feed from the catalog.
	if err := store.DeleteFeed(ctx, bob.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("catalog row survives DeleteFeed: %v", err)
	}
	for _, u := range []User{alice, bob} {
		var n int
		_ = store.db.QueryRow(ctx, `SELECT count(*) FROM user_entries WHERE user_id = $1 AND feed_id = $2`, u.ID, feed.ID).Scan(&n)
		if n != 0 {
			t.Fatalf("user %d keeps %d user_entries after catalog delete", u.ID, n)
		}
	}
	if _, total, _ := store.CatalogFeeds(ctx, alice.ID, nil, 50, 0); total != 1 {
		t.Fatalf("catalog after delete total=%d", total)
	}
}

// Following a category: the follower gets every feed of the category now
// and later (create, update, bulk move), loses feeds that leave it, cannot
// unsubscribe a single feed while following, and unfollow drops the feeds.
func TestIntegration_CategoryFollow(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	editor := newIntegrationUser(t, store, "cf_editor")
	reader := newIntegrationUser(t, store, "cf_reader")
	a, _ := store.CreateCategory(ctx, "A", "")
	b, _ := store.CreateCategory(ctx, "B", "")
	if _, err := store.CreateCategory(ctx, " a ", ""); !errors.Is(err, ErrDuplicateCategory) {
		t.Fatalf("case-insensitive duplicate title: %v", err)
	}
	if _, err := store.UpdateCategory(ctx, b.ID, "A", ""); !errors.Is(err, ErrDuplicateCategory) {
		t.Fatalf("rename to an existing title: %v", err)
	}

	f1, err := store.CreateFeed(ctx, editor.ID, CreateFeedParams{FeedURL: "https://example.com/cf/1", FeedType: "rss", Title: "f1", IntervalMinutes: 60, CategoryID: &a.ID})
	if err != nil {
		t.Fatal(err)
	}
	solo := newFeedForUser(t, store, editor.ID, "cf_solo", 60)
	if _, err := store.UpdateFeed(ctx, editor.ID, UpdateFeedParams{ID: solo.ID, FeedURL: solo.FeedURL, Title: solo.Title, IntervalMinutes: 60, CategoryID: &a.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Subscribe(ctx, reader.ID, solo.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}

	n, err := store.FollowCategory(ctx, reader.ID, a.ID)
	if err != nil || n != 1 {
		t.Fatalf("follow: n=%d err=%v (solo was already subscribed)", n, err)
	}
	if n, err := store.FollowCategory(ctx, reader.ID, a.ID); err != nil || n != 0 {
		t.Fatalf("double follow must be a no-op: n=%d err=%v", n, err)
	}
	if _, err := store.FollowCategory(ctx, reader.ID, a.ID+1000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("follow missing: %v", err)
	}
	if followed, _ := store.ListFollowedCategories(ctx, reader.ID); !followed[a.ID] || followed[b.ID] {
		t.Fatalf("followed: %v", followed)
	}
	if counts, _ := store.CategoryFollowerCounts(ctx); counts[a.ID] != 1 || counts[b.ID] != 0 {
		t.Fatalf("follower counts: %v", counts)
	}
	if err := store.Unsubscribe(ctx, reader.ID, f1.ID); !errors.Is(err, ErrFollowsCategory) {
		t.Fatalf("unsubscribe a followed feed: %v", err)
	}
	if err := store.DeleteCategory(ctx, a.ID); !errors.Is(err, ErrCategoryNotEmpty) {
		t.Fatalf("delete non-empty category: %v", err)
	}

	// New feed in A reaches the follower without the editor subscribing.
	f2, err := store.CreateFeed(ctx, editor.ID, CreateFeedParams{FeedURL: "https://example.com/cf/2", FeedType: "rss", Title: "f2", IntervalMinutes: 60, CategoryID: &a.ID, SkipOwnerSubscription: true})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := store.GetFeed(ctx, reader.ID, f2.ID); err != nil || !v.Subscribed {
		t.Fatalf("follower not subscribed to a new feed: %+v %v", v, err)
	}
	if _, err := store.GetFeed(ctx, editor.ID, f2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SkipOwnerSubscription ignored: %v", err)
	}

	// Feeds leaving A are dropped for its followers, also the one solo
	// subscription made before following: a follower reads the category's
	// feeds through the category.
	if _, err := store.UpdateFeed(ctx, editor.ID, UpdateFeedParams{ID: f1.ID, FeedURL: f1.FeedURL, Title: f1.Title, IntervalMinutes: 60, CategoryID: &b.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeed(ctx, reader.ID, f1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("follower keeps a feed that left the category: %v", err)
	}
	if _, n, err := store.BulkUpdateFeedsByCategory(ctx, editor.ID, a.ID, BulkFeedUpdate{MoveCategory: true, MoveToCategoryID: nil}); err != nil || n != 2 {
		t.Fatalf("bulk move to uncategorized: n=%d err=%v", n, err)
	}
	if _, err := store.GetFeed(ctx, reader.ID, f2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("follower keeps a bulk-moved feed: %v", err)
	}
	if _, err := store.GetFeed(ctx, reader.ID, solo.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("follower keeps a bulk-moved feed: %v", err)
	}
	if subs, _ := store.ListSubscriptions(ctx, editor.ID); len(subs) != 2 {
		t.Fatalf("non-follower subscriptions changed by the move: %+v", subs)
	}
	// Moving back into A re-subscribes the follower.
	if _, n, err := store.BulkUpdateFeedsByCategory(ctx, editor.ID, 0, BulkFeedUpdate{MoveCategory: true, MoveToCategoryID: &a.ID}); err != nil || n != 2 {
		t.Fatalf("bulk move back: n=%d err=%v", n, err)
	}
	if _, err := store.GetFeed(ctx, reader.ID, f2.ID); err != nil {
		t.Fatalf("follower not re-subscribed: %v", err)
	}

	n, err = store.UnfollowCategory(ctx, reader.ID, a.ID)
	if err != nil || n != 2 {
		t.Fatalf("unfollow: n=%d err=%v", n, err)
	}
	if _, err := store.UnfollowCategory(ctx, reader.ID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double unfollow: %v", err)
	}
	if subs, _ := store.ListSubscriptions(ctx, reader.ID); len(subs) != 0 {
		t.Fatalf("subscriptions after unfollow: %+v", subs)
	}

	// Deleting a user drops the follower row; deleting an emptied category works.
	if _, err := store.FollowCategory(ctx, reader.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteUser(ctx, reader.ID); err != nil {
		t.Fatal(err)
	}
	if counts, _ := store.CategoryFollowerCounts(ctx); counts[b.ID] != 0 {
		t.Fatalf("follower row survived user deletion: %v", counts)
	}
	if err := store.DeleteFeedByID(ctx, f1.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteCategory(ctx, b.ID); err != nil {
		t.Fatalf("delete empty category: %v", err)
	}
}

// A star set by any subscriber protects the shared entry from feed
// retention and from collapsing to a hash, not only the owner's star.
func TestIntegration_SharedFeed_OtherUsersStarProtectsEntry(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "sfs_alice")
	bob := newIntegrationUser(t, store, "sfs_bob")
	feed, entries := newIntegrationFeedWithEntries(t, store, alice.ID, 3)
	if _, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}
	starred := true
	if _, err := store.BulkUpdateEntries(ctx, bob.ID, []int64{entries[0].ID}, BulkEntryUpdate{Starred: &starred}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE entries SET created_at = now() - interval '40 days' WHERE feed_id = $1`, feed.ID); err != nil {
		t.Fatal(err)
	}
	days := 30
	if _, err := store.UpdateFeed(ctx, alice.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: feed.Title, IntervalMinutes: 60, EntryRetentionDays: &days}); err != nil {
		t.Fatal(err)
	}
	res, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{RemovedEntriesBefore: RetentionCutoff(time.Now(), 30), WebhookLogsBefore: RetentionCutoff(time.Now(), 90)})
	if err != nil {
		t.Fatal(err)
	}
	if res.FeedEntries != 2 {
		t.Fatalf("feed retention deleted %d entries, want 2 (bob's star keeps one)", res.FeedEntries)
	}
	if e, err := store.GetEntry(ctx, alice.ID, entries[0].ID); err != nil || e.Starred {
		t.Fatalf("alice should still see the entry bob starred, unstarred for her: %+v err=%v", e, err)
	}
	n, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{FeedID: &feed.ID, IncludeLabeled: true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("collapse removed %d entries, want 0", n)
	}
	if _, err := store.BulkUpdateEntries(ctx, bob.ID, []int64{entries[0].ID}, BulkEntryUpdate{Starred: ptrBool(false)}); err != nil {
		t.Fatal(err)
	}
	if n, _ = store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{FeedID: &feed.ID, IncludeLabeled: true}); n != 1 {
		t.Fatalf("collapse after unstar removed %d entries, want 1", n)
	}
	if known, _ := store.FilterKnownEntryHashes(ctx, feed.ID, []string{entries[0].Hash}); len(known) != 1 {
		t.Fatal("collapsed hash not kept for poll dedup")
	}
}

func ptrBool(b bool) *bool { return &b }
