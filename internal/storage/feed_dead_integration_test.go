//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

// Dead feeds (gone/blocked, no success for DeadFeedAfter) are skipped by the
// daily reset and get one control poll in the next 24 h; a recently
// successful feed with the same error is reset as before.
func TestIntegration_DailyReset_KeepsDeadFeeds(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := newIntegrationUser(t, store, "dead_owner")
	mk := func(url string) Feed {
		f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: url, FeedType: "rss", IntervalMinutes: 60})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	dead := mk("https://dead.example/a.xml")
	quota := mk("https://quota.example/a.xml")
	recent := mk("https://recent.example/a.xml")
	plain := mk("https://plain.example/a.xml")
	now := time.Now().Truncate(time.Second)
	old := now.Add(-DeadFeedAfter - time.Hour)
	// Every feed was created just now: age the three candidates.
	for _, f := range []Feed{dead, quota, recent} {
		if _, err := store.db.Exec(ctx, `UPDATE feeds SET created_at = $2 WHERE id = $1`, f.ID, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: recent.ID, LastCheckedAt: now.Add(-time.Hour), Succeeded: true, NextCheckAt: &now}); err != nil {
		t.Fatal(err)
	}
	fail := func(f Feed, msg string, n int) {
		for i := 0; i < n; i++ {
			if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: f.ID, Error: msg, CheckedAt: now, NextCheckAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	fail(dead, "fetch feed: unexpected status code: 404", 3)
	fail(quota, "maxstat api error insufficient_credits: Not enough credits", 1)
	fail(recent, "fetch feed: unexpected status code: 404", 3)
	fail(plain, "fetch feed: parse feed: failed to detect feed type", 2)

	list, err := store.ListDeadFeeds(ctx)
	if err != nil || len(list) != 2 || list[0].ID != dead.ID || !list[0].Gone || list[1].ID != quota.ID || list[1].Gone {
		t.Fatalf("dead feeds: %+v err=%v", list, err)
	}

	reset, kept, err := store.ResetFeedPollErrorsDaily(ctx)
	if err != nil || reset != 2 || kept != 2 {
		t.Fatalf("daily reset: reset=%d dead=%d err=%v", reset, kept, err)
	}
	for _, id := range []int64{recent.ID, plain.ID} {
		f, _ := store.GetFeedByID(ctx, id)
		if f.ParsingErrorCount != 0 || f.LastError != "" {
			t.Fatalf("live feed not reset: %+v", f)
		}
	}
	for _, id := range []int64{dead.ID, quota.ID} {
		f, _ := store.GetFeedByID(ctx, id)
		if f.ParsingErrorCount == 0 || f.LastError == "" || f.NextCheckAt == nil ||
			f.NextCheckAt.Before(time.Now().Add(-time.Second)) || f.NextCheckAt.After(time.Now().Add(24*time.Hour+time.Second)) {
			t.Fatalf("dead feed must keep its error and poll within a day: %+v", f)
		}
	}
	// Manually paused dead feeds are nobody's business.
	if err := store.SetFeedManualPaused(ctx, dead.ID, true); err == nil {
		if list, _ := store.ListDeadFeeds(ctx); len(list) != 1 {
			t.Fatalf("paused feed still dead: %+v", list)
		}
	}
}
