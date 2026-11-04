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
