//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_CollectDailySummary(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := newIntegrationUser(t, store, "sum_owner")
	rss, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://sum.example/a.xml", FeedType: "rss", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	tg, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://t.me/s/sumchan", FeedType: "telegram", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rec := func(f Feed, at time.Time, ok bool, inserted int, msg string) {
		if err := store.RecordFeedPoll(ctx, RecordFeedPollParams{FeedID: f.ID, At: at, OK: ok, Inserted: inserted, Error: msg}); err != nil {
			t.Fatal(err)
		}
	}
	rec(tg, now.Add(-3*time.Hour), false, 0, "x")
	rec(tg, now.Add(-2*time.Hour), false, 0, "x")  // coalesced into repeat_count
	rec(tg, now.Add(-30*time.Hour), false, 0, "y") // outside the window
	for i, at := range []time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour), now.Add(-30 * time.Hour)} {
		if _, err := store.db.Exec(ctx, `INSERT INTO feed_entry_dedup (feed_id, hash, first_seen_at) VALUES ($1, $2, $3)`, rss.ID, []byte{byte(i), 1, 2}, at); err != nil {
			t.Fatal(err)
		}
	}

	sum, err := store.CollectDailySummary(ctx, now.Add(-24*time.Hour), now, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DailySummaryTypeRow{}
	for _, r := range sum.ByType {
		by[r.FeedType] = r
	}
	if r := by["rss"]; r.Inserted != 2 || r.ErrorPolls != 0 || r.Feeds != 1 {
		t.Fatalf("rss row: %+v", r)
	}
	if r := by["telegram"]; r.Inserted != 0 || r.ErrorPolls != 2 || r.ErrorFeeds != 1 {
		t.Fatalf("telegram row: %+v", r)
	}
	if sum.Feeds.TotalFeeds != 2 || sum.Counters.DBBytes <= 0 || sum.Counters.WALBytes <= 0 {
		t.Fatalf("summary: feeds=%+v counters=%+v", sum.Feeds, sum.Counters)
	}
}
