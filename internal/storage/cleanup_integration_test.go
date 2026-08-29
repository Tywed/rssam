//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

func TestIntegration_FeedEntryRetentionCleanup(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{
		Username:     "retention_" + suffix,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	retention := 7
	feed, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
		FeedURL:            "https://example.com/retention/" + suffix + ".xml",
		FeedType:           "rss",
		Title:              "Retention feed",
		IntervalMinutes:    60,
		EntryRetentionDays: &retention,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.Exec(context.Background(), `DELETE FROM feeds WHERE id = $1`, feed.ID)
	})

	oldTime := time.Now().UTC().Add(-10 * 24 * time.Hour)
	newTime := time.Now().UTC().Add(-1 * 24 * time.Hour)

	n, inserted, err := store.CreateEntries(ctx, feed.ID, []CreateEntryParams{{
		Title: "Old entry",
		URL:   "https://example.com/old-" + suffix,
		Hash:  "old-" + suffix,
	}, {
		Title: "New entry",
		URL:   "https://example.com/new-" + suffix,
		Hash:  "new-" + suffix,
	}})
	if err != nil {
		t.Fatalf("create entries: %v", err)
	}
	if n != 2 || len(inserted) != 2 {
		t.Fatalf("inserted=%d len=%d", n, len(inserted))
	}
	oldEntryID, newEntryID := inserted[0].ID, inserted[1].ID

	if _, err := store.db.Exec(ctx, `UPDATE entries SET created_at = $2 WHERE id = $1`, oldEntryID, oldTime); err != nil {
		t.Fatalf("backdate old entry: %v", err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE entries SET created_at = $2 WHERE id = $1`, newEntryID, newTime); err != nil {
		t.Fatalf("backdate new entry: %v", err)
	}

	if _, err := store.RecordFeedEntryDedup(ctx, feed.ID, []FeedEntryDedupParams{
		{Hash: "dedup-old-" + suffix, URL: "https://example.com/dedup-old"},
		{Hash: "dedup-new-" + suffix, URL: "https://example.com/dedup-new"},
	}); err != nil {
		t.Fatalf("record dedup: %v", err)
	}
	if _, err := store.db.Exec(ctx, `
UPDATE feed_entry_dedup SET first_seen_at = $3 WHERE feed_id = $1 AND hash = $2`,
		feed.ID, "dedup-old-"+suffix, oldTime); err != nil {
		t.Fatalf("backdate old dedup: %v", err)
	}
	if _, err := store.db.Exec(ctx, `
UPDATE feed_entry_dedup SET first_seen_at = $3 WHERE feed_id = $1 AND hash = $2`,
		feed.ID, "dedup-new-"+suffix, newTime); err != nil {
		t.Fatalf("backdate new dedup: %v", err)
	}

	result, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{})
	if err != nil {
		t.Fatalf("retention cleanup: %v", err)
	}
	if result.FeedEntries != 1 {
		t.Fatalf("feed entries deleted=%d want 1", result.FeedEntries)
	}
	if result.FeedEntryDedup != 1 {
		t.Fatalf("feed dedup deleted=%d want 1", result.FeedEntryDedup)
	}

	if _, err := store.GetEntry(ctx, u.ID, oldEntryID); err != ErrNotFound {
		t.Fatalf("old entry should be deleted: err=%v", err)
	}
	if _, err := store.GetEntry(ctx, u.ID, newEntryID); err != nil {
		t.Fatalf("new entry should remain: %v", err)
	}

	var dedupCount int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_entry_dedup WHERE feed_id = $1`, feed.ID).Scan(&dedupCount); err != nil {
		t.Fatalf("count dedup: %v", err)
	}
	if dedupCount != 1 {
		t.Fatalf("dedup rows=%d want 1", dedupCount)
	}
}
