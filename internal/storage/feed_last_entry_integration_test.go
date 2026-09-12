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
