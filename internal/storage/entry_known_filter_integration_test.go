//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

func TestIntegration_CreateEntriesDropsKnownAndItemsHash(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "known", PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://known.example/rss", FeedType: "rss", Title: "k", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(i int) CreateEntryParams {
		return CreateEntryParams{Title: fmt.Sprintf("n%d", i), URL: fmt.Sprintf("https://known.example/%d", i), Content: "c", Hash: fmt.Sprintf("k%d", i)}
	}
	if _, err := store.RecordFeedEntryDedup(ctx, f.ID, []FeedEntryDedupParams{{Hash: "k3"}}); err != nil {
		t.Fatal(err)
	}
	n, got, err := store.CreateEntries(ctx, f.ID, []CreateEntryParams{mk(1), mk(2), mk(3)})
	if err != nil || n != 2 || len(got) != 2 {
		t.Fatalf("first: n=%d len=%d err=%v (k3 is in feed_entry_dedup)", n, len(got), err)
	}
	// Repeat plus one new: only the new one comes back.
	n, got, err = store.CreateEntries(ctx, f.ID, []CreateEntryParams{mk(1), mk(2), mk(3), mk(4)})
	if err != nil || n != 1 || len(got) != 1 || got[0].Hash != "k4" {
		t.Fatalf("second: n=%d got=%+v err=%v", n, got, err)
	}
	// All known: nothing inserted, no error, no rows.
	n, got, err = store.CreateEntries(ctx, f.ID, []CreateEntryParams{mk(1), mk(4)})
	if err != nil || n != 0 || len(got) != 0 {
		t.Fatalf("third: n=%d got=%+v err=%v", n, got, err)
	}
	if _, _, err := store.CreateEntries(ctx, f.ID, []CreateEntryParams{{Title: "x", URL: "https://known.example/x"}}); err == nil {
		t.Fatal("entry without hash must be rejected")
	}

	// items_hash: set, kept on nil, reset when the feed URL changes.
	h := "abc123"
	now := time.Now().UTC()
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: f.ID, LastCheckedAt: now, ItemsHash: &h}); err != nil {
		t.Fatal(err)
	}
	if fd, _ := store.GetFeedByID(ctx, f.ID); fd.ItemsHash != h {
		t.Fatalf("items_hash=%q, want %q", fd.ItemsHash, h)
	}
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: f.ID, LastCheckedAt: now}); err != nil {
		t.Fatal(err)
	}
	if fd, _ := store.GetFeedByID(ctx, f.ID); fd.ItemsHash != h {
		t.Fatalf("nil must keep items_hash, got %q", fd.ItemsHash)
	}
	fd, _ := store.GetFeedByID(ctx, f.ID)
	if _, err := store.UpdateFeed(ctx, u.ID, UpdateFeedParams{ID: f.ID, FeedURL: fd.FeedURL, Title: "renamed", IntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	if fd, _ = store.GetFeedByID(ctx, f.ID); fd.ItemsHash != h {
		t.Fatalf("same URL must keep items_hash, got %q", fd.ItemsHash)
	}
	if _, err := store.UpdateFeed(ctx, u.ID, UpdateFeedParams{ID: f.ID, FeedURL: "https://known.example/other", Title: "renamed", IntervalMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	if fd, _ = store.GetFeedByID(ctx, f.ID); fd.ItemsHash != "" {
		t.Fatalf("new URL must reset items_hash, got %q", fd.ItemsHash)
	}
}
