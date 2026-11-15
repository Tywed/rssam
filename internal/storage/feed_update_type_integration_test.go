//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
)

func TestIntegration_UpdateFeedURLChangesTypeAndInboundIsImmutable(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "feed-type")

	feed, err := store.CreateFeed(ctx, owner.ID, CreateFeedParams{FeedURL: "https://example.com/a.xml", FeedType: "rss", Title: "a", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	upd := func(url, typ string) (Feed, error) {
		return store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: feed.ID, FeedURL: url, FeedType: typ, Title: "a", IntervalMinutes: 60})
	}
	// Same URL: the detected type passed by the caller is ignored.
	f, err := upd("https://example.com/a.xml", "telegram")
	if err != nil || f.FeedType != "rss" {
		t.Fatalf("same URL: type=%q err=%v", f.FeedType, err)
	}
	f, err = upd("https://t.me/s/channel", "telegram")
	if err != nil || f.FeedType != "telegram" {
		t.Fatalf("new URL: type=%q err=%v", f.FeedType, err)
	}

	in, _, err := store.CreateInboundFeed(ctx, owner.ID, CreateInboundFeedParams{Title: "in"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: in.ID, FeedURL: in.FeedURL, FeedType: "rss", Title: "renamed", IntervalMinutes: 60}); err != nil {
		t.Fatalf("inbound rename: %v", err)
	}
	_, err = store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: in.ID, FeedURL: "https://example.com/b.xml", FeedType: "rss", Title: "in", IntervalMinutes: 60})
	if !errors.Is(err, ErrInboundURLImmutable) {
		t.Fatalf("inbound URL change: %v", err)
	}
}
