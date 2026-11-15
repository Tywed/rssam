//go:build integration

package storage

import (
	"context"
	"testing"
	"time"
)

// A feed row always has its poll-state row, however it was inserted, and
// the due wake-up (0057, moved by 0069) still fires for the three ways a
// feed becomes due earlier than planned.
func TestIntegration_FeedPollStateRowAndDueNotify(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := testUser(t, store, ctx, "pollstate")

	var feedID int64
	if err := store.db.QueryRow(ctx, `
INSERT INTO feeds(owner_id, feed_url, feed_type, title, interval_minutes)
VALUES ($1, 'https://example.com/ps.xml', 'rss', 'ps', 60) RETURNING id`, u.ID).Scan(&feedID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_poll_state WHERE feed_id = $1`, feedID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("state rows for a raw insert: %d err=%v", n, err)
	}
	created, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://example.com/ps2.xml", FeedType: "max", Title: "ps2", IntervalMinutes: 60, BridgeState: []byte(`{"max":{"last_end_time_ms":7}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetFeedByID(ctx, created.ID); err != nil || string(got.BridgeState) != `{"max": {"last_end_time_ms": 7}}` {
		t.Fatalf("bridge_state on create: %s err=%v", got.BridgeState, err)
	}

	far := time.Now().Add(2 * time.Hour)
	if err := store.SetFeedNextCheckAt(ctx, feedID, far); err != nil {
		t.Fatal(err)
	}

	listenCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	got := make(chan string, 16)
	ready := make(chan struct{})
	go func() {
		_ = store.ListenDue(listenCtx, func(ch string) {
			select {
			case got <- ch:
			default:
			}
		})
	}()
	// LISTEN is issued on a fresh connection; give it a moment.
	go func() { time.Sleep(300 * time.Millisecond); close(ready) }()
	<-ready

	expect := func(what string) {
		t.Helper()
		select {
		case ch := <-got:
			if ch != NotifyFeedsDue {
				t.Fatalf("%s: channel %s", what, ch)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: no due notification", what)
		}
	}
	// 1. Made due earlier than planned.
	if err := store.SetFeedNextCheckAt(ctx, feedID, time.Now()); err != nil {
		t.Fatal(err)
	}
	expect("earlier next_check_at")
	// 2. Circuit breaker released.
	if _, err := store.db.Exec(ctx, `UPDATE feed_poll_state SET poll_paused = TRUE WHERE feed_id = $1`, feedID); err != nil {
		t.Fatal(err)
	}
	for len(got) > 0 {
		<-got
	}
	if err := store.ResetFeedPollCircuit(ctx, feedID); err != nil {
		t.Fatal(err)
	}
	expect("circuit reset")
	// 3. Manual unpause (a feeds column).
	if err := store.SetFeedManualPaused(ctx, feedID, true); err != nil {
		t.Fatal(err)
	}
	for len(got) > 0 {
		<-got
	}
	if err := store.SetFeedManualPaused(ctx, feedID, false); err != nil {
		t.Fatal(err)
	}
	expect("manual unpause")

	if _, err := store.db.Exec(ctx, `DELETE FROM feeds WHERE id = $1`, feedID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_poll_state WHERE feed_id = $1`, feedID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("state row after delete: %d err=%v", n, err)
	}
}

// feed_poll_state is UNLOGGED (0071); after a crash emptied it, every feed
// gets its row back and is polled (and listed) again.
func TestIntegration_FeedPollStateUnloggedAndRecreated(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := testUser(t, store, ctx, "unlogged")

	var persistence string
	if err := store.db.QueryRow(ctx, `SELECT relpersistence FROM pg_class WHERE relname = 'feed_poll_state'`).Scan(&persistence); err != nil || persistence != "u" {
		t.Fatalf("relpersistence=%q err=%v, want u (UNLOGGED)", persistence, err)
	}
	created, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://example.com/unl.xml", FeedType: "rss", Title: "unl", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := store.EnsureFeedPollStateRows(ctx); err != nil || n != 0 {
		t.Fatalf("nothing missing: n=%d err=%v", n, err)
	}
	// What a crash does to an UNLOGGED table.
	if _, err := store.db.Exec(ctx, `TRUNCATE feed_poll_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetFeedByID(ctx, created.ID); err == nil {
		t.Fatal("feed without a state row must not be found (inner join) — otherwise the test proves nothing")
	}
	n, err := store.EnsureFeedPollStateRows(ctx)
	if err != nil || n < 1 {
		t.Fatalf("re-created rows: n=%d err=%v", n, err)
	}
	got, err := store.GetFeedByID(ctx, created.ID)
	if err != nil || got.NextCheckAt == nil || got.ETag != "" || got.ParsingErrorCount != 0 {
		t.Fatalf("feed after re-creation: %+v err=%v", got, err)
	}
	// Re-created rows are spread over the next 30 minutes, not all due now.
	if d := time.Until(*got.NextCheckAt); d < -time.Second || d > 30*time.Minute {
		t.Fatalf("re-created next_check_at %v from now, want within 30 minutes", d)
	}
	if _, err := store.db.Exec(ctx, `UPDATE feed_poll_state SET next_check_at = now() WHERE feed_id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListFeedsDue(ctx, 100, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range due {
		found = found || d.ID == created.ID
	}
	if !found {
		t.Fatalf("re-created feed must be back on the schedule, got %+v", due)
	}
}

// The bridge document lives in a logged table; an unchanged document costs
// no row version, a changed one replaces it, and a successful poll without
// a document resets it to {}.
func TestIntegration_BridgeStateUpsert(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := testUser(t, store, ctx, "bstate")
	f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://example.com/bs.xml", FeedType: "rss", Title: "bs", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_bridge_state WHERE feed_id = $1`, f.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after create without state: %d err=%v", n, err)
	}
	now := time.Now()
	meta := func(state []byte) {
		t.Helper()
		if err := store.UpdateFeedRefreshMeta(ctx, UpdateFeedRefreshMetaParams{ID: f.ID, LastCheckedAt: now, BridgeState: state}); err != nil {
			t.Fatal(err)
		}
	}
	meta([]byte(`{"rss":{"body":"a"}}`))
	meta([]byte(`{"rss":{"body":"a"}}`))
	got, err := store.GetFeedByID(ctx, f.ID)
	if err != nil || string(got.BridgeState) != `{"rss": {"body": "a"}}` {
		t.Fatalf("state after two identical polls: %s err=%v", got.BridgeState, err)
	}
	// xmin/xmax of the row: a new version would change xmin, a row lock
	// (the ON CONFLICT ... DO UPDATE WHERE form) would set xmax.
	version := func() (string, string) {
		t.Helper()
		var xmin, xmax string
		if err := store.db.QueryRow(ctx, `SELECT xmin::text, xmax::text FROM feed_bridge_state WHERE feed_id = $1`, f.ID).Scan(&xmin, &xmax); err != nil {
			t.Fatal(err)
		}
		return xmin, xmax
	}
	xminBefore, _ := version()
	if err := upsertBridgeState(ctx, store.db, f.ID, []byte(`{"rss":{"body":"a"}}`)); err != nil {
		t.Fatal(err)
	}
	if xmin, xmax := version(); xmin != xminBefore || xmax != "0" {
		t.Fatalf("unchanged document touched the row: xmin %s -> %s, xmax %s", xminBefore, xmin, xmax)
	}
	meta(nil)
	if got, err := store.GetFeedByID(ctx, f.ID); err != nil || string(got.BridgeState) != `{}` {
		t.Fatalf("state after a poll without a document: %s err=%v", got.BridgeState, err)
	}
	if err := store.RecordFeedPollFailure(ctx, RecordFeedPollFailureParams{ID: f.ID, Error: "x", CheckedAt: now, NextCheckAt: now, BridgeState: []byte(`{"telegram":{"c":1}}`)}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetFeedByID(ctx, f.ID); err != nil || string(got.BridgeState) != `{"telegram": {"c": 1}}` {
		t.Fatalf("state after a failure with a document: %s err=%v", got.BridgeState, err)
	}
	if err := store.DeleteFeed(ctx, u.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_bridge_state WHERE feed_id = $1`, f.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rows after delete: %d err=%v", n, err)
	}
}
