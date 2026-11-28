//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// A feed with a steady rhythm that has gone silent for days is quiet; a feed
// that is merely between posts, one with too little history, one in error
// and one on pause are not. Items of one poll count as one event.
func TestIntegration_QuietFeeds(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := newIntegrationUser(t, store, "quiet_owner")
	mk := func(url string) Feed {
		f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: url, FeedType: "telegram", IntervalMinutes: 5, Title: url})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	quiet := mk("https://t.me/quiet")
	between := mk("https://t.me/between")
	young := mk("https://t.me/young")
	broken := mk("https://t.me/broken")
	paused := mk("https://t.me/paused")

	now := time.Now()
	// events: n polls with new items, `every` apart, the last one `ago` ago;
	// each poll brings three items seconds apart (one event, not three).
	seed := func(f Feed, n int, every, ago time.Duration) {
		for i := 0; i < n; i++ {
			at := now.Add(-ago - time.Duration(n-1-i)*every)
			for j := 0; j < 3; j++ {
				hash := fmt.Sprintf("%d-%d-%d", f.ID, i, j)
				if _, err := store.db.Exec(ctx, `INSERT INTO feed_entry_dedup (feed_id, hash, first_seen_at) VALUES ($1, sha256($2::bytea), $3)`,
					f.ID, hash, at.Add(time.Duration(j)*10*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	seed(quiet, 40, 40*time.Minute, 5*24*time.Hour)  // usual 40 min, silent 5 days
	seed(between, 40, 40*time.Minute, 3*time.Hour)   // 3 h > 3×p90? p90 = 40 min → 2 h; but under the 24 h floor
	seed(young, 10, 40*time.Minute, 5*24*time.Hour)  // rhythm not measurable (< 20 events)
	seed(broken, 40, 40*time.Minute, 5*24*time.Hour) // in error
	seed(paused, 40, 40*time.Minute, 5*24*time.Hour) // on manual pause
	if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: broken.ID, Error: "fetch feed: EOF", CheckedAt: now, NextCheckAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET manual_paused = TRUE WHERE id = $1`, paused.ID); err != nil {
		t.Fatal(err)
	}

	got, err := store.QuietFeeds(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FeedID != quiet.ID {
		t.Fatalf("quiet feeds: %+v, want only %d", got, quiet.ID)
	}
	q := got[0]
	if q.Events != 39 || q.MedianGap < 39*time.Minute || q.MedianGap > 40*time.Minute || q.QuietFor < 5*24*time.Hour-time.Minute || q.Title != "https://t.me/quiet" {
		t.Fatalf("quiet row: %+v", q)
	}

	// The admin filter and the summary agree.
	rows, total, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: "quiet", Limit: 10, SortKey: "id", Order: "asc"})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != quiet.ID {
		t.Fatalf("admin filter quiet: total=%d rows=%+v err=%v", total, rows, err)
	}
	sum, err := store.CollectDailySummary(ctx, now.Add(-24*time.Hour), now, 14*24*time.Hour, 0)
	if err != nil || len(sum.Quiet) != 1 || sum.Quiet[0].FeedID != quiet.ID {
		t.Fatalf("summary quiet: %+v err=%v", sum.Quiet, err)
	}
}

// The "pause dead feeds" button stops only dead feeds without a success for
// DeadFeedArchiveAfter; a feed that died last week keeps its control poll.
func TestIntegration_PauseDeadFeeds(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := newIntegrationUser(t, store, "dead_archive_owner")
	mk := func(url string, age time.Duration) Feed {
		f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: url, FeedType: "rss", IntervalMinutes: 60})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(ctx, `UPDATE feeds SET created_at = now() - $2::interval WHERE id = $1`, f.ID, age.String()); err != nil {
			t.Fatal(err)
		}
		return f
	}
	old := mk("https://old.example/a.xml", 40*24*time.Hour)
	fresh := mk("https://fresh.example/a.xml", 7*24*time.Hour)
	now := time.Now()
	for _, f := range []Feed{old, fresh} {
		for i := 0; i < 3; i++ {
			if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: f.ID, Error: "fetch feed: unexpected status code: 404", CheckedAt: now, NextCheckAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	sum, err := store.AdminFeedSummary(ctx, 0, 0)
	if err != nil || sum.DeadCount != 2 || sum.DeadOldCount != 1 {
		t.Fatalf("summary dead=%d old=%d err=%v", sum.DeadCount, sum.DeadOldCount, err)
	}
	n, err := store.PauseDeadFeeds(ctx, DeadFeedArchiveAfter)
	if err != nil || n != 1 {
		t.Fatalf("pause dead: n=%d err=%v", n, err)
	}
	rows, _, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: "dead", Limit: 10, SortKey: "id", Order: "asc"})
	if err != nil || len(rows) != 1 || rows[0].ID != fresh.ID {
		t.Fatalf("dead after pause: %+v err=%v", rows, err)
	}
	if n, _ := store.PauseDeadFeeds(ctx, DeadFeedArchiveAfter); n != 0 {
		t.Fatalf("second pass paused %d", n)
	}
}
