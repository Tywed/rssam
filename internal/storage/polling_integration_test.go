//go:build integration

package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func dueIDs(due []DueFeed) []int64 {
	out := make([]int64, 0, len(due))
	for _, d := range due {
		out = append(out, d.ID)
	}
	return out
}

// Due discovery without a queue table: a feed is due until its poll moves
// next_check_at, the dispatcher's in-flight ids are excluded, and the
// backlog counters follow.
func TestIntegration_PollDueDiscovery(t *testing.T) {
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

	due1, err := store.ListFeedsDue(ctx, 100, nil)
	if err != nil || len(due1) != 1 || due1[0].ID != due.ID || due1[0].DueAt.IsZero() {
		t.Fatalf("due feeds = %v err=%v, want [%d]", due1, err, due.ID)
	}
	if got, _ := store.ListFeedsDue(ctx, 0, nil); got != nil {
		t.Fatalf("limit 0 must return nothing, got %v", got)
	}
	// An in-flight poll keeps the feed due in the table; the dispatcher
	// excludes it by id and the idle wait must not be woken by it either.
	if got, _ := store.ListFeedsDue(ctx, 100, []int64{due.ID}); len(got) != 0 {
		t.Fatalf("excluded feed listed as due: %v", dueIDs(got))
	}
	next, known, err := store.NextFeedDueAt(ctx, []int64{due.ID})
	if err != nil || !known || time.Until(next) < 55*time.Minute {
		t.Fatalf("next due with in-flight excluded = %s known=%v err=%v", next, known, err)
	}
	counts, err := store.PollFeedJobCounts(ctx)
	if err != nil || counts.Pending != 1 || counts.Overdue != 1 || counts.Running != 0 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}

	// A poll outcome moves next_check_at and the feed stops being due.
	if err := store.SetFeedNextCheckAt(ctx, due.ID, time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.ListFeedsDue(ctx, 100, nil); len(got) != 0 {
		t.Fatalf("polled feed still due: %v", dueIDs(got))
	}
	next, known, err = store.NextFeedDueAt(ctx, nil)
	if err != nil || !known || time.Until(next) > 10*time.Minute || time.Until(next) < 9*time.Minute {
		t.Fatalf("next due = %s known=%v err=%v", next, known, err)
	}
	counts, _ = store.PollFeedJobCounts(ctx)
	if counts.Pending != 0 || counts.Overdue != 0 || counts.MaxLagSeconds != 0 {
		t.Fatalf("counts after poll = %+v", counts)
	}
	// Making the feed due again brings it back, oldest due first.
	if err := store.SetFeedNextCheckAt(ctx, due.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeedNextCheckAt(ctx, later.ID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := store.ListFeedsDue(ctx, 100, nil)
	if ids := dueIDs(got); len(ids) != 2 || ids[0] != later.ID || ids[1] != due.ID {
		t.Fatalf("due order = %v, want [%d %d]", ids, later.ID, due.ID)
	}
	counts, _ = store.PollFeedJobCounts(ctx)
	if counts.Pending != 2 || counts.MaxLagSeconds < 3500 {
		t.Fatalf("counts with backlog = %+v", counts)
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

	if err := store.SetFeedNextCheckAt(ctx, healthy.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	feeds, queued, err := store.RefreshAllFeeds(ctx)
	if err != nil || feeds != 2 || queued != 2 {
		t.Fatalf("feeds=%d queued=%d err=%v", feeds, queued, err)
	}
	after, _ := store.GetFeedByID(ctx, tripped.ID)
	if after.PollPaused || after.ParsingErrorCount != 0 || after.LastError != "down" {
		t.Fatalf("refresh-all must clear the circuit but keep last_error: %+v", after)
	}
	for _, f := range []Feed{healthy, tripped} {
		got, _ := store.GetFeedByID(ctx, f.ID)
		if got.NextCheckAt == nil || time.Until(*got.NextCheckAt) > RefreshAllSpread(2) {
			t.Fatalf("feed %d not made due: next_check_at=%v", f.ID, got.NextCheckAt)
		}
	}
	m, _ := store.GetFeedByID(ctx, manual.ID)
	if !m.ManualPaused {
		t.Fatal("manual pause lost")
	}
	if got, _ := store.ListFeedsDue(ctx, 100, nil); len(got) != 0 && got[0].ID == manual.ID {
		t.Fatalf("manually paused feed due: %v", dueIDs(got))
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

	sum, err := store.AdminFeedSummary(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.TotalFeeds != 4 || sum.OKCount != 1 || sum.ErrorCount != 1 || sum.PausedCount != 1 || sum.WaitingCount != 1 || sum.TotalUnread != 2 {
		t.Fatalf("summary=%+v", sum)
	}

	all, _, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Limit: 10})
	if err != nil || len(all) != 4 {
		t.Fatalf("list all: n=%d err=%v", len(all), err)
	}
	if all[0].ID != errored.ID || all[1].ID != ok.ID || all[2].ID != paused.ID {
		t.Fatalf("default order is by title: %v", []int64{all[0].ID, all[1].ID, all[2].ID, all[3].ID})
	}
	for _, row := range all {
		switch row.ID {
		case waiting.ID:
			if row.EntryCount != 3 || row.UnreadCount != 2 {
				t.Fatalf("waiting row=%+v", row)
			}
		default:
			if row.EntryCount != 0 {
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

func TestIntegration_GetAdminFeedRow(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "adminrow")
	feed := newFeedForUser(t, store, owner.ID, "adminrow", 60)
	_, created, err := store.CreateEntries(ctx, feed.ID, []CreateEntryParams{
		{Title: "a", URL: "https://example.com/ar/1", Hash: "ar-1"},
		{Title: "b", URL: "https://example.com/ar/2", Hash: "ar-2"},
	})
	if err != nil || len(created) != 2 {
		t.Fatalf("create entries: %v err=%v", created, err)
	}
	if _, err := store.UpdateEntry(ctx, owner.ID, feed.ID, created[1].ID, UpdateEntryParams{Status: ptr(EntryStatusRead)}); err != nil {
		t.Fatal(err)
	}
	row, err := store.GetAdminFeedRow(ctx, feed.ID)
	if err != nil || row.ID != feed.ID || row.EntryCount != 2 || row.UnreadCount != 1 || row.SubscriberCount != 1 {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if _, err := store.GetAdminFeedRow(ctx, feed.ID+1_000_000); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing feed: %v", err)
	}
}

func TestIntegration_FeedTypeStatsAndLayoutFilter(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "admin")

	fine := newFeedForUser(t, store, owner.ID, "fine", 30)
	broken := newFeedForUser(t, store, owner.ID, "broken", 30)
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET feed_type = 'telegram' WHERE id = ANY($1)`, []int64{fine.ID, broken.ID}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	next := at.Add(time.Hour)
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: broken.ID, LastCheckedAt: at, LastError: LayoutErrorPrefix + "tgme_channel_info", NextCheckAt: &next}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.FeedTypeStats(ctx)
	if err != nil || len(stats) != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	st := stats[0]
	if st.FeedType != "telegram" || st.Total != 2 || st.Layout != 1 || st.LayoutMarker != "tgme_channel_info" || st.LayoutAt == nil || !st.LayoutAt.Equal(at) {
		t.Fatalf("stat=%+v", st)
	}

	rows, total, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: "layout", Limit: 10})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != broken.ID {
		t.Fatalf("layout filter: total=%d rows=%d err=%v", total, len(rows), err)
	}

	// A good poll clears the marker; a feed in error keeps its counter.
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: broken.ID, LastCheckedAt: at.Add(time.Minute), NextCheckAt: &next}); err != nil {
		t.Fatal(err)
	}
	stats, err = store.FeedTypeStats(ctx)
	if err != nil || stats[0].Layout != 0 || stats[0].LayoutAt != nil || stats[0].LayoutMarker != "" {
		t.Fatalf("after recovery: %+v err=%v", stats[0], err)
	}
}
