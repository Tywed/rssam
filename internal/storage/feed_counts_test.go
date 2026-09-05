//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

func testUser(t *testing.T, store *PostgresStore, ctx context.Context, prefix string) User {
	t.Helper()
	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{
		Username:     prefix + fmt.Sprintf("%d", time.Now().UnixNano()),
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })
	return u
}

func TestIntegration_FeedCountsByCategory(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	u := testUser(t, store, ctx, "counts_")

	cat1, err := store.CreateCategory(ctx, u.ID, "Telegram", "")
	if err != nil {
		t.Fatal(err)
	}
	cat2, err := store.CreateCategory(ctx, u.ID, "VK", "")
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
			FeedURL:         fmt.Sprintf("https://example.com/tg-%d-%d.xml", u.ID, i),
			Title:           "TG",
			IntervalMinutes: 60,
			CategoryID:      &cat1.ID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
		FeedURL:         fmt.Sprintf("https://example.com/vk-%d.xml", u.ID),
		Title:           "VK",
		IntervalMinutes: 60,
		CategoryID:      &cat2.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
		FeedURL:         fmt.Sprintf("https://example.com/uncat-%d.xml", u.ID),
		Title:           "Uncat",
		IntervalMinutes: 60,
	}); err != nil {
		t.Fatal(err)
	}

	counts, err := store.FeedCountsByCategory(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 5 {
		t.Fatalf("total=%d, want 5", counts.Total)
	}
	if counts.ByCategory[cat1.ID] != 3 {
		t.Fatalf("cat1=%d, want 3", counts.ByCategory[cat1.ID])
	}
	if counts.ByCategory[cat2.ID] != 1 {
		t.Fatalf("cat2=%d, want 1", counts.ByCategory[cat2.ID])
	}
	if counts.Uncategorized != 1 {
		t.Fatalf("uncategorized=%d, want 1", counts.Uncategorized)
	}
}

func TestIntegration_CountFeedStatuses(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	u := testUser(t, store, ctx, "status_")

	f1, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
		FeedURL:         fmt.Sprintf("https://example.com/good-%d.xml", u.ID),
		Title:           "Good",
		IntervalMinutes: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	f2, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
		FeedURL:         fmt.Sprintf("https://example.com/bad-%d.xml", u.ID),
		Title:           "Bad",
		IntervalMinutes: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFeedPollFailure(ctx, f2.ID, "timeout", 2, f2.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeedManualPaused(ctx, f1.ID, true); err != nil {
		t.Fatal(err)
	}

	errors, inactive, err := store.CountFeedStatuses(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if errors != 2 {
		t.Fatalf("errors=%d, want 2", errors)
	}
	if inactive != 1 {
		t.Fatalf("inactive=%d, want 1", inactive)
	}
}

func TestIntegration_ListFeedsNoLimit(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	u := testUser(t, store, ctx, "nolimit_")

	for i := 0; i < 5; i++ {
		if _, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
			FeedURL:         fmt.Sprintf("https://example.com/feed-%d-%d.xml", u.ID, i),
			Title:           "Feed",
			IntervalMinutes: 60,
		}); err != nil {
			t.Fatal(err)
		}
	}

	feeds, total, err := store.ListFeeds(ctx, u.ID, NoLimit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(feeds) != 5 {
		t.Fatalf("got %d feeds total=%d, want 5", len(feeds), total)
	}
}
