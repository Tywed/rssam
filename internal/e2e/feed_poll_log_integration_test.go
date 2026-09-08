//go:build integration

package e2e

import (
	"context"
	"testing"
	"time"

	"rssam/internal/storage"
)

// TestE2E_FeedPollLog_FailureThenRecovery: the worker polls a feed whose URL
// 404s (failure row, error count 1, status event with the persisted state),
// then the feed URL is fixed and the next poll succeeds (success row with the
// inserted count, counters cleared). Both attempts stay in feed_poll_log.
func TestE2E_FeedPollLog_FailureThenRecovery(t *testing.T) {
	env := newEnv(t, map[string]string{
		"/good.xml": rssDocument("Poll log good", []string{"one", "two"}),
	})
	ctx := context.Background()

	// Points at a path the feed server does not know → HTTP 404.
	feed := env.createFeed(t, "/missing.xml", nil)

	waitFor(t, "first (failed) poll to be logged", func() bool {
		rows, err := env.store.ListFeedPollLog(ctx, feed.ID, 10)
		return err == nil && len(rows) >= 1
	})
	rows, err := env.store.ListFeedPollLog(ctx, feed.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].OK || rows[0].Error == "" || rows[0].Inserted != 0 {
		t.Fatalf("failed poll row = %+v", rows[0])
	}
	after, err := env.store.GetFeedByID(ctx, feed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ParsingErrorCount != 1 || after.LastError == "" || after.NextCheckAt == nil {
		t.Fatalf("feed after failure = count=%d err=%q next=%v", after.ParsingErrorCount, after.LastError, after.NextCheckAt)
	}
	events := env.status.forFeed(feed.ID)
	if len(events) != 1 || events[0].Err == nil || events[0].Feed.ParsingErrorCount != 1 || events[0].Feed.NextCheckAt == nil {
		t.Fatalf("status events after failure = %+v", events)
	}
	// The event must describe the same schedule that is persisted (Postgres
	// keeps microseconds, the event carries nanoseconds).
	if d := events[0].Feed.NextCheckAt.Sub(*after.NextCheckAt); d < -time.Millisecond || d > time.Millisecond {
		t.Fatalf("event next_check_at=%s, db=%s", events[0].Feed.NextCheckAt, after.NextCheckAt)
	}
	// And the queued job must run at exactly that time.
	job, err := env.store.GetPollFeedJob(ctx, feed.ID)
	if err != nil || job == nil {
		t.Fatalf("poll job: %+v err=%v", job, err)
	}
	if !job.RunAt.Equal(*after.NextCheckAt) {
		t.Fatalf("job.run_at=%s, feeds.next_check_at=%s", job.RunAt, after.NextCheckAt)
	}
	if job.LastError == nil || *job.LastError == "" {
		t.Fatal("job.last_error must carry the failure")
	}

	// Fix the URL and make the feed due again.
	if _, err := env.store.UpdateFeed(ctx, env.user.ID, storage.UpdateFeedParams{
		ID:              feed.ID,
		FeedURL:         env.feeds.URL + "/good.xml",
		Title:           feed.Title,
		IntervalMinutes: feed.IntervalMinutes,
	}); err != nil {
		t.Fatalf("update feed: %v", err)
	}
	// The failed job is parked until next_check_at (10m × 2); pull it forward
	// the same way "refresh all" does.
	if err := env.store.EnqueuePollFeedJob(ctx, feed.ID, time.Now().UTC().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "second (successful) poll to be logged", func() bool {
		rows, err := env.store.ListFeedPollLog(ctx, feed.ID, 10)
		return err == nil && len(rows) >= 2 && rows[0].OK
	})
	rows, err = env.store.ListFeedPollLog(ctx, feed.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Inserted != 2 || rows[0].Error != "" || rows[0].DurationMS < 0 {
		t.Fatalf("success row = %+v", rows[0])
	}
	if rows[1].OK {
		t.Fatalf("history must keep the earlier failure: %+v", rows)
	}
	after, err = env.store.GetFeedByID(ctx, feed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ParsingErrorCount != 0 || after.LastError != "" || after.PollPaused {
		t.Fatalf("feed after recovery = %+v", after)
	}
	events = env.status.forFeed(feed.ID)
	last := events[len(events)-1]
	if last.Err != nil || last.Feed.ParsingErrorCount != 0 || last.Feed.PollPaused || last.Feed.LastError != "" {
		t.Fatalf("status event after recovery = %+v", last)
	}
}
