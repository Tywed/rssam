//go:build integration

package storage

import (
	"context"
	"testing"

	"rssam/internal/migrations"
)

// 0072 folds vk_search and vk_group into vk and moves the search cursor to
// the "vk" bridge_state key; 0073 then carries the documents into
// feed_bridge_state. Both run twice without effect the second time, and
// the old type names are no longer insertable.
func TestIntegration_Migration0072_0073_VKMergeBridgeState(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	u := testUser(t, store, ctx, "m72")
	// Restore the pre-0072 constraint and the pre-0073 column so the
	// legacy rows can be inserted.
	for _, q := range []string{
		`ALTER TABLE feeds DROP CONSTRAINT IF EXISTS feeds_feed_type_check`,
		`ALTER TABLE feeds ADD CONSTRAINT feeds_feed_type_check CHECK (feed_type IN ('rss', 'atom', 'json', 'telegram', 'vk', 'vk_search', 'vk_group', 'max', 'maxstat', 'rutube', 'dzen_news', 'dzen_channel', 'ok_group', 'smotrim', 'page', 'custom', 'inbound'))`,
		`ALTER TABLE feed_poll_state ADD COLUMN bridge_state JSONB NOT NULL DEFAULT '{}'`,
	} {
		if _, err := store.db.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	search, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "vk-search://m72", FeedType: "vk_search", Title: "s", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	wall, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://vk.com/m72", FeedType: "vk_group", Title: "w", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `UPDATE feed_poll_state SET bridge_state = '{"vk_search":{"q":"m72","last_end_time":150},"rss":{"body":"x"}}' WHERE feed_id = $1`, search.ID); err != nil {
		t.Fatal(err)
	}
	m72, err := migrations.Source("0072_feed_type_vk_merge.sql")
	if err != nil {
		t.Fatal(err)
	}
	m73, err := migrations.Source("0073_feed_bridge_state.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, m72); err != nil {
			t.Fatalf("0072 run %d: %v", run, err)
		}
		var st string
		if err := store.db.QueryRow(ctx, `SELECT bridge_state::text FROM feed_poll_state WHERE feed_id = $1`, search.ID).Scan(&st); err != nil || st != `{"vk": {"q": "m72", "last_end_time": 150}, "rss": {"body": "x"}}` {
			t.Fatalf("0072 run %d: search state=%s err=%v", run, st, err)
		}
	}
	for run := 1; run <= 2; run++ {
		if _, err := store.db.Exec(ctx, m73); err != nil {
			t.Fatalf("0073 run %d: %v", run, err)
		}
		s, err := store.GetFeedByID(ctx, search.ID)
		if err != nil || s.FeedType != "vk" || string(s.BridgeState) != `{"vk": {"q": "m72", "last_end_time": 150}, "rss": {"body": "x"}}` {
			t.Fatalf("0073 run %d: search feed: type=%s state=%s err=%v", run, s.FeedType, s.BridgeState, err)
		}
		w, err := store.GetFeedByID(ctx, wall.ID)
		if err != nil || w.FeedType != "vk" || string(w.BridgeState) != `{}` {
			t.Fatalf("0073 run %d: wall feed: type=%s state=%s err=%v", run, w.FeedType, w.BridgeState, err)
		}
		var rows int
		if err := store.db.QueryRow(ctx, `SELECT count(*) FROM feed_bridge_state WHERE feed_id IN ($1, $2)`, search.ID, wall.ID).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("0073 run %d: bridge rows %d err=%v (empty documents must not be copied)", run, rows, err)
		}
	}
	for _, old := range []string{"vk_search", "vk_group"} {
		if _, err := store.db.Exec(ctx, `UPDATE feeds SET feed_type = $1 WHERE id = $2`, old, wall.ID); err == nil {
			t.Fatalf("%s still allowed by the constraint", old)
		}
	}
}
