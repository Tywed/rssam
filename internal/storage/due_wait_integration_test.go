//go:build integration

package storage

import (
	"context"
	"testing"
	"time"

	"rssam/internal/auth"
)

func TestIntegration_DueNotifications(t *testing.T) {
	store := isolatedStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan string, 16)
	go func() { _ = store.ListenDue(ctx, func(ch string) { got <- ch }) }()
	// LISTEN must be in place before the first write.
	time.Sleep(300 * time.Millisecond)

	// Channels are database-wide, so other test packages on the same
	// database may raise notifications too: drain until ours shows up.
	expect := func(what, channel string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case ch := <-got:
				if ch == channel {
					return
				}
			case <-deadline:
				t.Fatalf("%s: no %q notification", what, channel)
			}
		}
	}

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "due", PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://due.example/rss", FeedType: "rss", Title: "d", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	expect("create feed", NotifyFeedsDue)

	future := time.Now().Add(time.Hour)
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: f.ID, LastCheckedAt: time.Now(), NextCheckAt: &future}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.NextFeedDueAt(ctx); err != nil || !ok {
		t.Fatalf("NextFeedDueAt: ok=%v err=%v", ok, err)
	}
	if err := store.SetFeedNextCheckAt(ctx, f.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	expect("next_check_at moved to now", NotifyFeedsDue)

	if err := store.EnqueuePollFeedJobs(ctx, []int64{f.ID}, time.Now()); err != nil {
		t.Fatal(err)
	}
	expect("job enqueued", NotifyJobsDue)
	if at, ok, err := store.NextJobRunAt(ctx); err != nil || !ok || at.After(time.Now().Add(time.Minute)) {
		t.Fatalf("NextJobRunAt: %v ok=%v err=%v", at, ok, err)
	}
	if _, ok, err := store.NextWebhookRetryAt(ctx); err != nil || ok {
		t.Fatalf("NextWebhookRetryAt on empty table: ok=%v err=%v", ok, err)
	}
}
