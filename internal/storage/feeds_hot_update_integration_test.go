//go:build integration

package storage

import (
	"context"
	"testing"
	"time"

	"rssam/internal/auth"
)

// The poll bookkeeping UPDATE must stay HOT: no index on a column it writes
// (migration 0056 dropped feeds_due_poll_idx) and page slack to land in.
func TestIntegration_FeedPollUpdateIsHOT(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "hot", PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.CreateFeed(ctx, u.ID, CreateFeedParams{FeedURL: "https://hot.example/rss", FeedType: "rss", Title: "h", IntervalMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}

	tx, err := store.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i := range 5 {
		next := time.Now().Add(time.Duration(i+1) * time.Minute)
		if _, err := tx.Exec(ctx, `
UPDATE feeds SET etag = $2, last_checked_at = now(), last_error = '', bridge_state = '{"rss":{"body":"x"}}',
  parsing_error_count = 0, poll_paused = FALSE, next_check_at = $3, items_hash = 'abc', updated_at = now()
WHERE id = $1`, f.ID, "e", next); err != nil {
			t.Fatal(err)
		}
	}
	var upd, hot int64
	if err := tx.QueryRow(ctx, `SELECT pg_stat_get_xact_tuples_updated('feeds'::regclass), pg_stat_get_xact_tuples_hot_updated('feeds'::regclass)`).Scan(&upd, &hot); err != nil {
		t.Fatal(err)
	}
	if upd != 5 || hot != 5 {
		t.Fatalf("poll updates: %d total, %d HOT; want all HOT", upd, hot)
	}
}
