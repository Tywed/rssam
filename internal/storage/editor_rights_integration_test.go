//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
)

// A shared feed is protected from its non-author subscribers: delete only
// without other readers, URL change only by the author or an admin, hash
// erasure only when the acting user is the sole reader.
func TestIntegration_EditorRightsOnSharedFeed(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "er_alice")
	bob := newIntegrationUser(t, store, "er_bob")

	feed, entries := newIntegrationFeedWithEntries(t, store, alice.ID, 3)
	if _, err := store.Subscribe(ctx, bob.ID, feed.ID, SubscriptionParams{}); err != nil {
		t.Fatal(err)
	}

	// Delete by a subscriber who is not alone: refused with the count.
	err := store.DeleteFeed(ctx, bob.ID, feed.ID)
	var inUse *FeedInUseError
	if !errors.As(err, &inUse) || inUse.Subscribers != 1 {
		t.Fatalf("delete with another subscriber: err=%v", err)
	}
	if _, err := store.GetFeedByID(ctx, feed.ID); err != nil {
		t.Fatalf("feed must survive: %v", err)
	}

	// URL change: bob is not the author.
	base := UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL + "?v=2", Title: feed.Title, IntervalMinutes: 60}
	if _, err := store.UpdateFeed(ctx, bob.ID, base); !errors.Is(err, ErrNotFeedOwner) {
		t.Fatalf("url change by non-author: err=%v", err)
	}
	// Same URL, other columns: allowed for every subscriber.
	if _, err := store.UpdateFeed(ctx, bob.ID, UpdateFeedParams{ID: feed.ID, FeedURL: feed.FeedURL, Title: "bob's title", IntervalMinutes: 60}); err != nil {
		t.Fatalf("title change by subscriber: %v", err)
	}
	// Admin override.
	adminParams := base
	adminParams.Admin = true
	if _, err := store.UpdateFeed(ctx, bob.ID, adminParams); err != nil {
		t.Fatalf("url change by admin: %v", err)
	}
	// Author.
	authorParams := base
	authorParams.FeedURL = feed.FeedURL + "?v=3"
	if _, err := store.UpdateFeed(ctx, alice.ID, authorParams); err != nil {
		t.Fatalf("url change by author: %v", err)
	}

	// Hash erasure by one of two readers: payload stays, caller falls back.
	if err := store.StripEntryPayloadAfterWebhook(ctx, entries[0].ID, bob.ID); !errors.Is(err, ErrEntryShared) {
		t.Fatalf("strip on shared feed: err=%v", err)
	}
	if e, err := store.GetEntry(ctx, alice.ID, entries[0].ID); err != nil || e.Title == "" {
		t.Fatalf("payload erased on shared feed: %+v err=%v", e, err)
	}
	if err := store.StripEntryPayloadAfterWebhook(ctx, 999999999, bob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("strip missing entry: err=%v", err)
	}
	// Bulk collapse restricted to sole-subscriber feeds touches nothing here.
	if n, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{UserID: bob.ID, FeedID: &feed.ID, IncludeLabeled: true, OnlySoleSubscriber: true}); err != nil || n != 0 {
		t.Fatalf("collapse on shared feed: n=%d err=%v", n, err)
	}

	// Bob leaves; alice is alone again and everything is permitted.
	if err := store.Unsubscribe(ctx, bob.ID, feed.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.StripEntryPayloadAfterWebhook(ctx, entries[0].ID, alice.ID); err != nil {
		t.Fatalf("strip as sole reader: %v", err)
	}
	if n, err := store.CollapseEntriesToHashes(ctx, CollapseEntriesParams{UserID: alice.ID, FeedID: &feed.ID, IncludeLabeled: true, OnlySoleSubscriber: true}); err != nil || n != 3 {
		t.Fatalf("collapse as sole reader: n=%d err=%v", n, err)
	}
	if err := store.DeleteFeed(ctx, alice.ID, feed.ID); err != nil {
		t.Fatalf("delete as sole reader: %v", err)
	}
	if err := store.DeleteFeed(ctx, alice.ID, feed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: err=%v", err)
	}
}
