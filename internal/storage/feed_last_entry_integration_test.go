//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

func TestIntegration_FeedLastEntryAtAndSilent(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "silent")
	week := 7 * 24 * time.Hour

	quiet := newFeedForUser(t, store, owner.ID, "quiet", 60)
	lively, _ := newIntegrationFeedWithEntries(t, store, owner.ID, 1)
	fresh := newFeedForUser(t, store, owner.ID, "fresh", 60)
	paused := newFeedForUser(t, store, owner.ID, "paused", 60)
	if err := store.SetFeedManualPaused(ctx, paused.ID, true); err != nil {
		t.Fatal(err)
	}
	// quiet/paused were created "long ago" and never delivered; lively got an
	// entry 10 days ago; fresh is a brand-new subscription.
	old := time.Now().Add(-30 * 24 * time.Hour)
	tenDays := time.Now().Add(-10 * 24 * time.Hour)
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET created_at = $1, last_entry_at = NULL WHERE id = ANY($2)`, old, []int64{quiet.ID, lively.ID, paused.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET last_entry_at = $1 WHERE id = $2`, tenDays, lively.ID); err != nil {
		t.Fatal(err)
	}

	// Migration backfill semantics: a NULL last_entry_at is derived from the
	// newest entry / dedup row. Re-run the statement of 0039 on this schema.
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET last_entry_at = NULL WHERE id = $1`, lively.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE feeds f SET last_entry_at = GREATEST(
  (SELECT max(e.created_at) FROM entries e WHERE e.feed_id = f.id),
  (SELECT max(d.first_seen_at) FROM feed_entry_dedup d WHERE d.feed_id = f.id))
WHERE f.last_entry_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetFeedByID(ctx, lively.ID)
	if err != nil || got.LastEntryAt == nil || time.Since(*got.LastEntryAt) > time.Minute {
		t.Fatalf("backfill from entries: %+v err=%v", got.LastEntryAt, err)
	}
	if got, _ := store.GetFeedByID(ctx, quiet.ID); got.LastEntryAt != nil {
		t.Fatalf("feed without entries must stay NULL: %v", *got.LastEntryAt)
	}
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET last_entry_at = $1 WHERE id = $2`, tenDays, lively.ID); err != nil {
		t.Fatal(err)
	}

	sum, err := store.AdminFeedSummary(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	if sum.SilentCount != 2 {
		t.Fatalf("silent = quiet + lively (paused excluded, fresh too young): %+v", sum)
	}
	if sum, _ := store.AdminFeedSummary(ctx, 0); sum.SilentCount != 0 {
		t.Fatalf("window 0 disables: %+v", sum)
	}
	rows, total, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: "silent", SortKey: "last_entry", Order: "desc", SilentAfter: week})
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("list silent: n=%d total=%d err=%v", len(rows), total, err)
	}
	if rows[0].ID != lively.ID || rows[1].ID != quiet.ID {
		t.Fatalf("last_entry desc puts NULL last: %d,%d", rows[0].ID, rows[1].ID)
	}
	if rows, total, _ := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: "silent"}); total != 0 || len(rows) != 0 {
		t.Fatalf("silent without window: %d", total)
	}

	// A poll with new items stamps last_entry_at in the same UPDATE; a poll
	// without new items leaves it alone.
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: quiet.ID, LastCheckedAt: now, NewEntries: 3}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetFeedByID(ctx, quiet.ID)
	if got.LastEntryAt == nil || !got.LastEntryAt.Equal(now) {
		t.Fatalf("stamped last_entry_at: %v want %v", got.LastEntryAt, now)
	}
	later := now.Add(time.Hour)
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: quiet.ID, LastCheckedAt: later, NewEntries: 0}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetFeedByID(ctx, quiet.ID)
	if !got.LastEntryAt.Equal(now) || !got.LastCheckedAt.Equal(later) {
		t.Fatalf("empty poll must not touch last_entry_at: entry=%v checked=%v", got.LastEntryAt, got.LastCheckedAt)
	}
	if sum, _ := store.AdminFeedSummary(ctx, week); sum.SilentCount != 1 {
		t.Fatalf("quiet is no longer silent: %+v", sum)
	}
	_ = fresh
}

func TestIntegration_InboundFeedSilentByInterval(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "inbound-silent")

	hourly, _, err := store.CreateInboundFeed(ctx, owner.ID, CreateInboundFeedParams{Title: "hourly", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	daily, _, err := store.CreateInboundFeed(ctx, owner.ID, CreateInboundFeedParams{Title: "daily"})
	if err != nil {
		t.Fatal(err)
	}
	if daily.IntervalMinutes != InboundDefaultIntervalMinutes {
		t.Fatalf("default interval: %d", daily.IntervalMinutes)
	}
	// Both received their last delivery 3 hours ago: hourly is late, daily
	// is within its expected pause.
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET created_at = now() - interval '2 days', last_entry_at = now() - interval '3 hours' WHERE id = ANY($1)`, []int64{hourly.ID, daily.ID}); err != nil {
		t.Fatal(err)
	}

	// The global window is off: polled feeds are never silent, the inbound
	// feed is judged by its own interval.
	sum, err := store.AdminFeedSummary(ctx, 0)
	if err != nil || sum.SilentCount != 1 {
		t.Fatalf("summary: %+v err=%v", sum, err)
	}
	rows, total, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: "silent"})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != hourly.ID || !rows[0].InboundLate {
		t.Fatalf("list silent: total=%d rows=%+v err=%v", total, rows, err)
	}
	rows, _, err = store.ListAdminFeedsPage(ctx, AdminFeedsListParams{SortKey: "id", Order: "asc"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.InboundLate != (r.ID == hourly.ID) {
			t.Fatalf("InboundLate on feed %d = %v", r.ID, r.InboundLate)
		}
	}
	silent, err := store.ListSilentFeeds(ctx, 0)
	if err != nil || len(silent) != 1 || silent[0].ID != hourly.ID || silent[0].Error != "нет доставок дольше 60 мин." {
		t.Fatalf("alert list: %+v err=%v", silent, err)
	}

	// A delivery resets the clock.
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: hourly.ID, LastCheckedAt: time.Now(), NewEntries: 1}); err != nil {
		t.Fatal(err)
	}
	if sum, _ := store.AdminFeedSummary(ctx, 0); sum.SilentCount != 0 {
		t.Fatalf("after delivery: %+v", sum)
	}
	// The global window of 7 days does not apply to inbound feeds: 3 hours
	// of silence on the daily feed is fine, and the hourly feed, silent for
	// 2 hours, is late again.
	if _, err := store.db.Exec(ctx, `UPDATE feeds SET last_entry_at = now() - interval '2 hours' WHERE id = $1`, hourly.ID); err != nil {
		t.Fatal(err)
	}
	if sum, _ := store.AdminFeedSummary(ctx, 7*24*time.Hour); sum.SilentCount != 1 {
		t.Fatalf("with window: %+v", sum)
	}
}
