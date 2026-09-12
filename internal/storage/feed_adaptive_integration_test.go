//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestIntegration_AdaptiveIntervalFlagAndActivity(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "adaptive")

	feed, err := store.CreateFeed(ctx, owner.ID, CreateFeedParams{
		FeedURL: fmt.Sprintf("https://example.com/adaptive/%d.xml", time.Now().UnixNano()), FeedType: "rss", Title: "a", IntervalMinutes: 30,
		AdaptiveInterval: true,
	})
	if err != nil || !feed.AdaptiveInterval {
		t.Fatalf("create: %+v %v", feed.AdaptiveInterval, err)
	}
	got, err := store.GetFeedByID(ctx, feed.ID)
	if err != nil || !got.AdaptiveInterval {
		t.Fatalf("get: %v %v", got.AdaptiveInterval, err)
	}
	upd, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: "a", IntervalMinutes: 30})
	if err != nil || upd.AdaptiveInterval {
		t.Fatalf("update must clear the flag when omitted: %v %v", upd.AdaptiveInterval, err)
	}
	rows, err := store.ListAdminFeeds(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == feed.ID && r.AdaptiveInterval {
			t.Fatal("admin row still shows adaptive after update")
		}
	}

	cat, err := store.CreateCategory(ctx, owner.ID, "adaptive-cat", "#000")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateFeed(ctx, owner.ID, CreateFeedParams{
		FeedURL: fmt.Sprintf("https://example.com/adaptive2/%d.xml", time.Now().UnixNano()), FeedType: "rss", Title: "b", IntervalMinutes: 30, CategoryID: &cat.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	on := true
	if _, n, err := store.BulkUpdateFeedsByCategory(ctx, owner.ID, cat.ID, BulkFeedUpdate{AdaptiveInterval: &on}); err != nil || n != 1 {
		t.Fatalf("bulk: n=%d err=%v", n, err)
	}
	if got, _ := store.GetFeedByID(ctx, other.ID); !got.AdaptiveInterval {
		t.Fatal("bulk enable did not stick")
	}

	// Activity: 3 entries (one older than the window) + 2 dedup rows.
	params := []CreateEntryParams{
		{Title: "1", URL: "https://example.com/a/1", Hash: "a1"},
		{Title: "2", URL: "https://example.com/a/2", Hash: "a2"},
		{Title: "3", URL: "https://example.com/a/3", Hash: "a3"},
	}
	if _, _, err := store.CreateEntries(ctx, feed.ID, params); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE entries SET created_at = now() - interval '10 days' WHERE feed_id = $1 AND hash = 'a1'`, feed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordFeedEntryDedup(ctx, feed.ID, []FeedEntryDedupParams{{Hash: "d1"}, {Hash: "d2"}}); err != nil {
		t.Fatal(err)
	}
	n, err := store.CountFeedItemsSince(ctx, feed.ID, time.Now().Add(-AdaptivePollWindow))
	if err != nil || n != 4 {
		t.Fatalf("items in window: %d %v (want 4)", n, err)
	}
	n, err = store.CountFeedItemsSince(ctx, other.ID, time.Now().Add(-AdaptivePollWindow))
	if err != nil || n != 0 {
		t.Fatalf("empty feed: %d %v", n, err)
	}
}
