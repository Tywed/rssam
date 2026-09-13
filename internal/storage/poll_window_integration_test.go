//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIntegration_CategoryPollHours(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "ph")
	other := newIntegrationUser(t, store, "ph_other")

	cat, err := store.CreateCategory(ctx, owner.ID, "Night", "")
	if err != nil {
		t.Fatal(err)
	}
	if cat.PollHours != "" {
		t.Fatalf("new category poll_hours=%q, want empty", cat.PollHours)
	}
	if got, err := store.GetCategoryPollHours(ctx, cat.ID); err != nil || got != "" {
		t.Fatalf("get: %q err=%v", got, err)
	}
	if err := store.SetCategoryPollHours(ctx, other.ID, cat.ID, "08:00-22:00"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign set: err=%v", err)
	}
	if err := store.SetCategoryPollHours(ctx, owner.ID, cat.ID, "8:00-22:00"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetCategoryPollHours(ctx, cat.ID); got != "08:00-22:00" {
		t.Fatalf("stored %q, want normalized", got)
	}
	if err := store.SetCategoryPollHours(ctx, owner.ID, cat.ID, "nope"); err == nil {
		t.Fatal("invalid value must be rejected")
	}
	cats, _, err := store.ListCategories(ctx, owner.ID, 10, 0)
	if err != nil || len(cats) != 1 || cats[0].PollHours != "08:00-22:00" {
		t.Fatalf("list: %+v err=%v", cats, err)
	}
	upd, err := store.UpdateCategory(ctx, owner.ID, cat.ID, "Night2", "")
	if err != nil || upd.PollHours != "08:00-22:00" {
		t.Fatalf("title/color update must keep poll_hours: %+v err=%v", upd, err)
	}

	// A feed parked far in the future by a window comes back to its own
	// interval when the window changes; a normally scheduled feed is untouched.
	parked := newFeedForUser(t, store, owner.ID, "parked", 30)
	normal := newFeedForUser(t, store, owner.ID, "normal", 30)
	for _, f := range []Feed{parked, normal} {
		if _, err := store.UpdateFeed(ctx, owner.ID, UpdateFeedParams{ID: f.ID, FeedURL: f.FeedURL, Title: f.Title, IntervalMinutes: 30, CategoryID: &cat.ID}); err != nil {
			t.Fatal(err)
		}
	}
	far := time.Now().Add(9 * time.Hour)
	if err := store.SetFeedNextCheckAt(ctx, parked.ID, far); err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(10 * time.Minute)
	if err := store.SetFeedNextCheckAt(ctx, normal.ID, soon); err != nil {
		t.Fatal(err)
	}
	// Same value → no write, feeds untouched.
	if err := store.SetCategoryPollHours(ctx, owner.ID, cat.ID, "08:00-22:00"); err != nil {
		t.Fatal(err)
	}
	if f, _ := store.GetFeedByID(ctx, parked.ID); f.NextCheckAt == nil || f.NextCheckAt.Sub(far).Abs() > time.Second {
		t.Fatalf("unchanged window must not touch feeds: %v", f.NextCheckAt)
	}
	if err := store.SetCategoryPollHours(ctx, owner.ID, cat.ID, ""); err != nil {
		t.Fatal(err)
	}
	if f, _ := store.GetFeedByID(ctx, parked.ID); f.NextCheckAt == nil || time.Until(*f.NextCheckAt) > 31*time.Minute {
		t.Fatalf("parked feed must be pulled back to its interval: %v", f.NextCheckAt)
	}
	if f, _ := store.GetFeedByID(ctx, normal.ID); f.NextCheckAt == nil || f.NextCheckAt.Sub(soon).Abs() > time.Second {
		t.Fatalf("normal feed must stay: %v", f.NextCheckAt)
	}
	if got, _ := store.GetCategoryPollHours(ctx, cat.ID); got != "" {
		t.Fatalf("cleared: %q", got)
	}
	if _, err := store.GetCategoryPollHours(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing category: err=%v", err)
	}
}
