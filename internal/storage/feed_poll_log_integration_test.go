//go:build integration

package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"rssam/internal/auth"
)

func TestIntegration_FeedPollLogRecordListRetention(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "polllog_" + suffix, PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	feed, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{
		FeedURL:         "https://example.com/polllog/" + suffix + ".xml",
		FeedType:        "rss",
		Title:           "Poll log feed",
		IntervalMinutes: 60,
	})
	if err != nil {
		t.Fatalf("create feed: %v", err)
	}

	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	longErr := strings.Repeat("x", MaxFeedPollLogError+500)
	rows := []RecordFeedPollParams{
		{FeedID: feed.ID, At: old, OK: true, Inserted: 5, Duration: 300 * time.Millisecond},
		{FeedID: feed.ID, At: now.Add(-2 * time.Minute), OK: false, Error: longErr, Duration: 2 * time.Second},
		{FeedID: feed.ID, At: now.Add(-time.Minute), OK: true, Inserted: 1, Duration: -time.Second},
	}
	for _, r := range rows {
		if err := store.RecordFeedPoll(ctx, r); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	got, err := store.ListFeedPollLog(ctx, feed.ID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d, want 3", len(got))
	}
	// Newest first.
	if !got[0].OK || got[0].Inserted != 1 || got[0].DurationMS != 0 {
		t.Fatalf("row0=%+v (negative duration must clamp to 0)", got[0])
	}
	if got[1].OK || len(got[1].Error) != MaxFeedPollLogError || got[1].DurationMS != 2000 {
		t.Fatalf("row1: ok=%v errlen=%d dur=%d", got[1].OK, len(got[1].Error), got[1].DurationMS)
	}
	if !got[2].OK || got[2].Inserted != 5 || got[2].DurationMS != 300 {
		t.Fatalf("row2=%+v", got[2])
	}

	limited, err := store.ListFeedPollLog(ctx, feed.ID, 2)
	if err != nil || len(limited) != 2 || limited[0].ID != got[0].ID {
		t.Fatalf("limit: len=%d err=%v", len(limited), err)
	}

	// Retention: zero cutoff skips the table; a 14-day cutoff drops the old row.
	res, err := store.RunRetentionCleanup(ctx, RetentionCleanupOpts{
		RemovedEntriesBefore: RetentionCutoff(now, 3650),
		WebhookLogsBefore:    RetentionCutoff(now, 3650),
	})
	if err != nil {
		t.Fatalf("cleanup (no poll log cutoff): %v", err)
	}
	if res.FeedPollLog != 0 {
		t.Fatalf("FeedPollLog=%d, want 0 when cutoff is zero", res.FeedPollLog)
	}
	res, err = store.RunRetentionCleanup(ctx, RetentionCleanupOpts{
		RemovedEntriesBefore: RetentionCutoff(now, 3650),
		WebhookLogsBefore:    RetentionCutoff(now, 3650),
		FeedPollLogBefore:    RetentionCutoff(now, 14),
	})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if res.FeedPollLog < 1 {
		t.Fatalf("FeedPollLog=%d, want >=1", res.FeedPollLog)
	}
	after, err := store.ListFeedPollLog(ctx, feed.ID, 10)
	if err != nil || len(after) != 2 {
		t.Fatalf("after cleanup len=%d err=%v", len(after), err)
	}

	// Deleting the feed cascades.
	if err := store.DeleteFeed(ctx, u.ID, feed.ID); err != nil {
		t.Fatalf("delete feed: %v", err)
	}
	var n int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_poll_log WHERE feed_id = $1`, feed.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("poll log rows after feed delete=%d, want 0", n)
	}
}

func TestIntegration_FeedPollLogCoalesceAndCap(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "pollcap")
	feed, err := store.CreateFeed(ctx, owner.ID, CreateFeedParams{
		FeedURL: "https://example.com/pollcap.xml", FeedType: "rss", Title: "cap", IntervalMinutes: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	ok := RecordFeedPollParams{FeedID: feed.ID, At: now, OK: true, Inserted: 1, Duration: time.Millisecond}
	if err := store.RecordFeedPoll(ctx, ok); err != nil {
		t.Fatal(err)
	}
	ok.At = now.Add(time.Minute)
	ok.Inserted = 2
	if err := store.RecordFeedPoll(ctx, ok); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListFeedPollLog(ctx, feed.ID, 10)
	if err != nil || len(got) != 1 || got[0].Inserted != 1 || got[0].RepeatCount != 1 {
		t.Fatalf("consecutive success must skip: %+v err=%v", got, err)
	}

	fail := RecordFeedPollParams{FeedID: feed.ID, At: now.Add(2 * time.Minute), OK: false, Error: "max: 400", Duration: time.Second}
	for i := 0; i < 5; i++ {
		fail.At = now.Add(time.Duration(2+i) * time.Minute)
		if err := store.RecordFeedPoll(ctx, fail); err != nil {
			t.Fatal(err)
		}
	}
	got, err = store.ListFeedPollLog(ctx, feed.ID, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("len=%d err=%v", len(got), err)
	}
	if got[0].OK || got[0].RepeatCount != 5 || got[0].Error != "max: 400" {
		t.Fatalf("coalesced failure: %+v", got[0])
	}

	for i := 0; i < MaxFeedPollLogPerFeed+5; i++ {
		p := RecordFeedPollParams{
			FeedID: feed.ID, At: now.Add(time.Duration(20+i) * time.Minute),
			OK: i%2 == 0, Error: fmt.Sprintf("e%d", i), Duration: time.Millisecond,
		}
		if err := store.RecordFeedPoll(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err = store.ListFeedPollLog(ctx, feed.ID, 100)
	if err != nil || len(got) != MaxFeedPollLogPerFeed {
		t.Fatalf("cap: len=%d want %d err=%v", len(got), MaxFeedPollLogPerFeed, err)
	}
}
