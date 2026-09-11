//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func jobIDs(jobs []Job) []int64 {
	out := make([]int64, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.ID)
	}
	return out
}

// The poll queue end to end: due feeds are discovered once, claimed once
// per instance, and a job ends in exactly one of complete/release/reschedule.
func TestIntegration_PollQueueLifecycle(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "queue")

	due := newFeedForUser(t, store, owner.ID, "due", 10)
	later := newFeedForUser(t, store, owner.ID, "later", 10)
	paused := newFeedForUser(t, store, owner.ID, "paused", 10)
	if err := store.SetFeedManualPaused(ctx, paused.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeedNextCheckAt(ctx, later.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	ids, err := store.ListFeedsDue(ctx, 100)
	if err != nil || len(ids) != 1 || ids[0] != due.ID {
		t.Fatalf("due feeds = %v err=%v, want [%d]", ids, err, due.ID)
	}
	if got, _ := store.ListFeedsDue(ctx, 0); got != nil {
		t.Fatalf("limit 0 must return nothing, got %v", got)
	}

	if err := store.EnqueuePollFeedJob(ctx, due.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Once queued the feed is no longer "due" for the scheduler.
	if ids, _ = store.ListFeedsDue(ctx, 100); len(ids) != 0 {
		t.Fatalf("queued feed listed as due: %v", ids)
	}
	// A second enqueue only moves run_at earlier, never later.
	if err := store.EnqueuePollFeedJob(ctx, due.ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetPollFeedJob(ctx, due.ID)
	if err != nil || job == nil || time.Until(job.RunAt) > 0 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if none, err := store.GetPollFeedJob(ctx, later.ID); err != nil || none != nil {
		t.Fatalf("no job expected for later feed: %+v err=%v", none, err)
	}
	counts, err := store.PollFeedJobCounts(ctx)
	if err != nil || counts.Pending != 1 || counts.Overdue != 1 || counts.Running != 0 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}

	a, err := store.ClaimDueJobs(ctx, 10, "inst-a")
	if err != nil || len(a) != 1 || a[0].FeedID == nil || *a[0].FeedID != due.ID || a[0].Type != "poll_feed" {
		t.Fatalf("claim a = %+v err=%v", a, err)
	}
	if b, _ := store.ClaimDueJobs(ctx, 10, "inst-b"); len(b) != 0 {
		t.Fatalf("second instance claimed a locked job: %v", jobIDs(b))
	}
	if none, _ := store.ClaimDueJobs(ctx, 0, "inst-a"); none != nil {
		t.Fatalf("limit 0 must claim nothing, got %v", jobIDs(none))
	}
	counts, _ = store.PollFeedJobCounts(ctx)
	if counts.Running != 1 || counts.Pending != 0 {
		t.Fatalf("counts after claim = %+v", counts)
	}

	// Only the locking instance may finish the job.
	if deleted, err := store.CompleteJob(ctx, a[0].ID, "inst-b"); err != nil || deleted {
		t.Fatalf("foreign complete: deleted=%v err=%v", deleted, err)
	}
	if err := store.RescheduleJob(ctx, a[0].ID, "inst-a", time.Now().Add(-time.Second), "boom"); err != nil {
		t.Fatal(err)
	}
	job, _ = store.GetPollFeedJob(ctx, due.ID)
	if job.Attempts != 1 || job.LastError == nil || *job.LastError != "boom" || job.LockedAt != nil {
		t.Fatalf("rescheduled job = %+v", job)
	}

	a, _ = store.ClaimDueJobs(ctx, 10, "inst-a")
	if len(a) != 1 {
		t.Fatalf("reclaim after reschedule: %v", jobIDs(a))
	}
	if err := store.ReleaseJob(ctx, a[0].ID, "inst-a"); err != nil {
		t.Fatal(err)
	}
	a, _ = store.ClaimDueJobs(ctx, 10, "inst-a")
	if len(a) != 1 {
		t.Fatalf("claim after release: %v", jobIDs(a))
	}
	if deleted, err := store.CompleteJob(ctx, a[0].ID, "inst-a"); err != nil || !deleted {
		t.Fatalf("complete: deleted=%v err=%v", deleted, err)
	}
	if job, _ = store.GetPollFeedJob(ctx, due.ID); job != nil {
		t.Fatalf("job survived completion: %+v", job)
	}
}

func TestIntegration_ReclaimStalePollJobs(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "stale")
	f1 := newFeedForUser(t, store, owner.ID, "f1", 10)
	f2 := newFeedForUser(t, store, owner.ID, "f2", 10)
	f3 := newFeedForUser(t, store, owner.ID, "f3", 10)

	if err := store.EnqueuePollFeedJobs(ctx, []int64{f1.ID, f2.ID, f3.ID}, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	// f1 locked by a dead instance, f2 by us long ago, f3 by us just now.
	lock := func(feedID int64, by string, age time.Duration) {
		t.Helper()
		if _, err := store.db.Exec(ctx, `UPDATE jobs SET locked_by = $2, locked_at = now() - $3::interval WHERE feed_id = $1`, feedID, by, age); err != nil {
			t.Fatal(err)
		}
	}
	lock(f1.ID, "dead-instance", time.Second)
	lock(f2.ID, "me", 10*time.Minute)
	lock(f3.ID, "me", time.Second)

	counts, err := store.PollFeedJobCounts(ctx)
	if err != nil || counts.Running != 3 || counts.Stale != 1 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}
	n, err := store.ReclaimStalePollJobs(ctx, "me", 2*time.Minute)
	if err != nil || n != 2 {
		t.Fatalf("reclaimed %d err=%v, want 2", n, err)
	}
	claimed, err := store.ClaimDueJobs(ctx, 10, "me")
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claimed %v err=%v, want the two reclaimed jobs", jobIDs(claimed), err)
	}
	// staleAfter <= 0 falls back to two minutes: f3 (locked 1 s ago) stays.
	if n, _ = store.ReclaimStalePollJobs(ctx, "me", 0); n != 0 {
		t.Fatalf("fresh own lock reclaimed: %d", n)
	}
}

func TestIntegration_RefreshAllResetsCircuitsAndQueuesEverythingButManual(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "refreshall")
	healthy := newFeedForUser(t, store, owner.ID, "healthy", 10)
	tripped := newFeedForUser(t, store, owner.ID, "tripped", 10)
	manual := newFeedForUser(t, store, owner.ID, "manual", 10)
	if err := store.SetFeedManualPaused(ctx, manual.ID, true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{
			ID: tripped.ID, Error: "down", Threshold: 3, CheckedAt: time.Now(), NextCheckAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := store.GetFeedByID(ctx, tripped.ID)
	if !before.PollPaused || before.ParsingErrorCount != 3 {
		t.Fatalf("circuit not tripped: %+v", before)
	}

	feeds, queued, err := store.EnqueueRefreshAllPollJobs(ctx)
	if err != nil || feeds != 2 || queued != 2 {
		t.Fatalf("feeds=%d queued=%d err=%v", feeds, queued, err)
	}
	after, _ := store.GetFeedByID(ctx, tripped.ID)
	if after.PollPaused || after.ParsingErrorCount != 0 || after.LastError != "down" {
		t.Fatalf("refresh-all must clear the circuit but keep last_error: %+v", after)
	}
	for _, f := range []Feed{healthy, tripped} {
		if job, _ := store.GetPollFeedJob(ctx, f.ID); job == nil {
			t.Fatalf("feed %d not queued", f.ID)
		}
	}
	if job, _ := store.GetPollFeedJob(ctx, manual.ID); job != nil {
		t.Fatalf("manually paused feed queued: %+v", job)
	}
	m, _ := store.GetFeedByID(ctx, manual.ID)
	if !m.ManualPaused {
		t.Fatal("manual pause lost")
	}
}

func TestIntegration_CircuitResetsAndDailyReset(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "circuit")
	errored := newFeedForUser(t, store, owner.ID, "errored", 10)
	tripped := newFeedForUser(t, store, owner.ID, "tripped", 10)
	gone := newFeedForUser(t, store, owner.ID, "gone", 10)
	clean := newFeedForUser(t, store, owner.ID, "clean", 10)

	fail := func(id int64, times int) {
		t.Helper()
		for i := 0; i < times; i++ {
			if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{
				ID: id, Error: "err", Threshold: 2, CheckedAt: time.Now(), NextCheckAt: time.Now().Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	fail(errored.ID, 1)
	fail(tripped.ID, 2)
	fail(gone.ID, 1)
	if err := store.SetFeedManualPaused(ctx, gone.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: 999999, Error: "x", CheckedAt: time.Now(), NextCheckAt: time.Now()}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing feed: err=%v", err)
	}

	if err := store.ResetFeedPollCircuit(ctx, tripped.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetFeedPollCircuit(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset missing feed: err=%v", err)
	}
	f, _ := store.GetFeedByID(ctx, tripped.ID)
	if f.PollPaused || f.ParsingErrorCount != 0 || f.LastError == "" {
		t.Fatalf("single reset: %+v", f)
	}

	fail(tripped.ID, 2)
	n, err := store.ResetErrorFeedPollCircuits(ctx)
	if err != nil || n != 3 {
		t.Fatalf("reset all: n=%d err=%v (errored, tripped, gone carry errors)", n, err)
	}
	g, _ := store.GetFeedByID(ctx, gone.ID)
	if !g.ManualPaused || g.ParsingErrorCount != 0 {
		t.Fatalf("bulk reset must keep manual pause: %+v", g)
	}

	// Daily reset: clears errors and makes feeds due again, except manual.
	fail(errored.ID, 1)
	fail(tripped.ID, 2)
	before := time.Now()
	n, err = store.ResetFeedPollErrorsDaily(ctx)
	if err != nil || n != 2 {
		t.Fatalf("daily reset: n=%d err=%v", n, err)
	}
	for _, id := range []int64{errored.ID, tripped.ID} {
		f, _ := store.GetFeedByID(ctx, id)
		if f.PollPaused || f.ParsingErrorCount != 0 || f.LastError != "" || f.NextCheckAt == nil || f.NextCheckAt.Before(before.Add(-time.Second)) || f.NextCheckAt.After(time.Now().Add(time.Second)) {
			t.Fatalf("after daily reset: %+v", f)
		}
	}
	g, _ = store.GetFeedByID(ctx, gone.ID)
	if !g.ManualPaused || g.LastError == "" {
		t.Fatalf("daily reset touched a manually paused feed: %+v", g)
	}
	c, _ := store.GetFeedByID(ctx, clean.ID)
	if c.LastError != "" || c.ParsingErrorCount != 0 {
		t.Fatalf("clean feed changed: %+v", c)
	}
}

func TestIntegration_AdminFeedDashboard(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "admin")

	ok := newFeedForUser(t, store, owner.ID, "b-ok", 30)
	errored := newFeedForUser(t, store, owner.ID, "a-errored", 30)
	paused := newFeedForUser(t, store, owner.ID, "c-paused", 30)
	waiting, entries := newIntegrationFeedWithEntries(t, store, owner.ID, 3)
	if _, err := store.BulkUpdateEntries(ctx, owner.ID, []int64{entries[0].ID}, BulkEntryUpdate{Status: ptr(EntryStatusRead)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeedNextCheckAt(ctx, ok.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: errored.ID, Error: "500", CheckedAt: time.Now(), NextCheckAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeedManualPaused(ctx, paused.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.EnqueuePollFeedJob(ctx, waiting.ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	sum, err := store.AdminFeedSummary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalFeeds != 4 || sum.OKCount != 1 || sum.ErrorCount != 1 || sum.PausedCount != 1 || sum.WaitingCount != 1 || sum.TotalUnread != 2 {
		t.Fatalf("summary=%+v", sum)
	}

	all, err := store.ListAdminFeeds(ctx, 0)
	if err != nil || len(all) != 4 {
		t.Fatalf("list all: n=%d err=%v", len(all), err)
	}
	if all[0].ID != errored.ID || all[1].ID != ok.ID || all[2].ID != paused.ID {
		t.Fatalf("default order is by title: %v", []int64{all[0].ID, all[1].ID, all[2].ID, all[3].ID})
	}
	for _, row := range all {
		switch row.ID {
		case waiting.ID:
			if row.EntryCount != 3 || row.UnreadCount != 2 || !row.HasQueuedJob {
				t.Fatalf("waiting row=%+v", row)
			}
		default:
			if row.EntryCount != 0 || row.HasQueuedJob {
				t.Fatalf("row %d=%+v", row.ID, row)
			}
		}
	}

	expect := map[string]int64{"ok": ok.ID, "errors": errored.ID, "paused": paused.ID, "waiting": waiting.ID}
	for status, want := range expect {
		rows, total, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: status})
		if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != want {
			t.Fatalf("status %q: rows=%d total=%d err=%v want feed %d", status, len(rows), total, err, want)
		}
	}
	rows, total, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Limit: 2, Offset: 2, SortKey: "id", Order: "asc"})
	if err != nil || total != 4 || len(rows) != 2 || rows[0].ID != paused.ID || rows[1].ID != waiting.ID {
		t.Fatalf("page 2 by id: total=%d ids=%v err=%v", total, feedIDs(rows), err)
	}
	for _, key := range []string{"status", "last_checked", "next_check", "errors", "entries", "unread", "title"} {
		for _, order := range []string{"asc", "desc"} {
			rows, total, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{SortKey: key, Order: order})
			if err != nil || total != 4 || len(rows) != 4 {
				t.Fatalf("sort %s %s: total=%d n=%d err=%v", key, order, total, len(rows), err)
			}
		}
	}
	rows, _, _ = store.ListAdminFeedsPage(ctx, AdminFeedsListParams{SortKey: "status", Order: "asc"})
	if rows[0].ID != paused.ID || rows[1].ID != errored.ID || rows[2].ID != waiting.ID || rows[3].ID != ok.ID {
		t.Fatalf("status order paused<errors<waiting<ok, got %v", feedIDs(rows))
	}
	rows, _, _ = store.ListAdminFeedsPage(ctx, AdminFeedsListParams{SortKey: "unread", Order: "desc"})
	if rows[0].ID != waiting.ID {
		t.Fatalf("unread desc first=%d want %d", rows[0].ID, waiting.ID)
	}

	offered, err := store.OfferedPollsPerMin(ctx)
	if want := 1.0/30 + 1.0/30 + 1.0/60; err != nil || offered < want-0.001 || offered > want+0.001 {
		t.Fatalf("offered=%v err=%v want %v (two 30-min feeds + one 60-min, paused excluded)", offered, err, want)
	}
	if n, err := store.DueWebhookLogCount(ctx); err != nil || n != 0 {
		t.Fatalf("due webhook logs=%d err=%v", n, err)
	}
	if size, err := store.EstimateDatabaseSize(ctx); err != nil || size <= 0 {
		t.Fatalf("db size=%d err=%v", size, err)
	}
}

func feedIDs(rows []AdminFeedRow) []int64 {
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func ptr[T any](v T) *T { return &v }
