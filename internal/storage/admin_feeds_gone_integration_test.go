//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

// Error shapes split into "gone" (source says the resource no longer
// exists, held for goneAfterErrors polls) and "blocked" (source refuses this
// client); last_success_at follows successful polls only.
func TestIntegration_AdminFeeds_GoneBlockedAndLastSuccess(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := newIntegrationUser(t, store, "gb_owner")
	mk := func(url string) Feed {
		f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: url, FeedType: "rss", IntervalMinutes: 60})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	gone := mk("https://gone.example/a.xml")
	once := mk("https://once.example/a.xml")
	blocked := mk("https://blocked.example/a.xml")
	tg := mk("https://t.me/s/vanished_channel")
	healthy := mk("https://ok.example/a.xml")
	now := time.Now().Truncate(time.Second)

	fail := func(f Feed, msg string, n int) {
		for i := 0; i < n; i++ {
			if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: f.ID, Error: msg, CheckedAt: now, NextCheckAt: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	fail(gone, "fetch feed: unexpected status code: 404", 3)
	fail(once, "fetch feed: unexpected status code: 404", 1)
	fail(blocked, "fetch feed: unexpected status code: 403", 1)
	fail(tg, "fetch feed: telegram: channel is private or does not exist", 5)

	// A real poll stamps last_success_at; a soft deferral (Succeeded=false,
	// same error text) only moves last_checked_at.
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: healthy.ID, LastCheckedAt: now, Succeeded: true, NextCheckAt: &now}); err != nil {
		t.Fatal(err)
	}
	later := now.Add(10 * time.Minute)
	if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: healthy.ID, LastCheckedAt: later, NextCheckAt: &later}); err != nil {
		t.Fatal(err)
	}
	h, err := store.GetFeedByID(ctx, healthy.ID)
	if err != nil || h.LastSuccessAt == nil || !h.LastSuccessAt.Equal(now) || h.LastCheckedAt == nil || !h.LastCheckedAt.Equal(later) {
		t.Fatalf("last_success_at=%v last_checked_at=%v err=%v", h.LastSuccessAt, h.LastCheckedAt, err)
	}
	if g, _ := store.GetFeedByID(ctx, gone.ID); g.LastSuccessAt != nil {
		t.Fatalf("failing feed got last_success_at=%v", g.LastSuccessAt)
	}

	sum, err := store.AdminFeedSummary(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sum.GoneCount != 2 || sum.BlockedCount != 1 || sum.ErrorCount != 4 {
		t.Fatalf("summary gone=%d blocked=%d errors=%d", sum.GoneCount, sum.BlockedCount, sum.ErrorCount)
	}
	ids := func(status string) map[int64]bool {
		rows, _, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: status, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		out := map[int64]bool{}
		for _, r := range rows {
			out[r.ID] = true
		}
		return out
	}
	if g := ids("gone"); len(g) != 2 || !g[gone.ID] || !g[tg.ID] {
		t.Fatalf("gone filter: %v", g)
	}
	if b := ids("blocked"); len(b) != 1 || !b[blocked.ID] {
		t.Fatalf("blocked filter: %v", b)
	}
	if rows, _, err := store.ListAdminFeedsPage(ctx, AdminFeedsListParams{Status: "all", SortKey: "last_success", Order: "desc", Limit: 50}); err != nil || rows[0].ID != healthy.ID {
		t.Fatalf("sort by last_success: %v err=%v", rows, err)
	}

	paused, err := store.ListPausedFeeds(ctx)
	if err != nil || len(paused) != 0 {
		t.Fatalf("nothing paused yet: %v %v", paused, err)
	}
	n, err := store.PauseGoneFeeds(ctx)
	if err != nil || n != 2 {
		t.Fatalf("pause gone: n=%d err=%v", n, err)
	}
	if n, _ := store.PauseGoneFeeds(ctx); n != 0 {
		t.Fatalf("second pause must be a no-op, got %d", n)
	}
	paused, err = store.ListPausedFeeds(ctx)
	if err != nil || len(paused) != 2 {
		t.Fatalf("paused after: %v %v", paused, err)
	}
	for _, p := range paused {
		if !p.Gone || p.Error == "" {
			t.Fatalf("paused gone feed must keep its error and flag: %+v", p)
		}
	}
	if g := ids("gone"); len(g) != 2 {
		t.Fatalf("gone filter keeps paused rows: %v", g)
	}
	if once, _ := store.GetFeedByID(ctx, once.ID); once.ManualPaused {
		t.Fatal("a single 404 must not be paused")
	}
}
