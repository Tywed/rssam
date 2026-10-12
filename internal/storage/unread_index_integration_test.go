//go:build integration

package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The unread counters (per feed, per category, per user, sidebar map) are
// index-only scans of user_unread's per-user indexes. Enough rows across
// several feeds so that the planner has a real choice.
func TestIntegration_UnreadCountersUseUnreadIndex(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "ui_alice")
	cat, _ := store.CreateCategory(ctx, "c", "")
	var feedIDs []int64
	for i := 0; i < 4; i++ {
		f, _ := newIntegrationFeedWithEntries(t, store, alice.ID, 300)
		if _, err := store.UpdateFeed(ctx, alice.ID, UpdateFeedParams{ID: f.ID, FeedURL: f.FeedURL, Title: f.Title, IntervalMinutes: 60, CategoryID: &cat.ID}); err != nil {
			t.Fatal(err)
		}
		feedIDs = append(feedIDs, f.ID)
	}
	// Another reader with more unread rows: a user_id condition must be
	// selective for a per-user index to beat a walk of the primary key.
	bob := newIntegrationUser(t, store, "ui_bob")
	for i := 0; i < 3; i++ {
		newIntegrationFeedWithEntries(t, store, bob.ID, 1000)
	}
	if _, err := store.db.Exec(ctx, `DELETE FROM user_unread WHERE user_id = $1 AND entry_id % 3 <> 0`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `VACUUM ANALYZE user_unread`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `ANALYZE feeds`); err != nil {
		t.Fatal(err)
	}
	// A few thousand rows fit in a few pages, where a sequential or bitmap
	// scan is as cheap as anything; the assertion is that the index-only
	// path exists.
	tx, err := store.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off; SET LOCAL enable_bitmapscan = off"); err != nil {
		t.Fatal(err)
	}
	plan := func(q string, args ...any) string {
		rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out.WriteString(line + "\n")
		}
		return out.String()
	}
	queries := map[string]string{
		"by feed":     fmt.Sprintf(`SELECT count(*) FROM user_unread WHERE user_id = %d AND feed_id = %d`, alice.ID, feedIDs[0]),
		"by category": fmt.Sprintf(`SELECT count(*) FROM user_unread uu JOIN feeds f ON f.id = uu.feed_id WHERE uu.user_id = %d AND f.category_id = %d`, alice.ID, cat.ID),
		"total":       fmt.Sprintf(`SELECT count(*) FROM user_unread WHERE user_id = %d`, alice.ID),
		"sidebar":     fmt.Sprintf(`SELECT feed_id, count(*) FROM user_unread WHERE user_id = %d GROUP BY feed_id`, alice.ID),
	}
	for name, q := range queries {
		if p := plan(q); !strings.Contains(p, "Index Only Scan using user_unread_user_") {
			t.Errorf("%s is not an index-only scan of a user_unread index:\n%s", name, p)
		}
	}
	for _, id := range feedIDs {
		n, err := store.CountUnreadByFeed(ctx, alice.ID, id)
		if err != nil || n != 100 {
			t.Fatalf("feed %d unread = %d err=%v", id, n, err)
		}
	}
	if n, _ := store.CountUnreadByCategory(ctx, alice.ID, cat.ID); n != 400 {
		t.Fatalf("category unread = %d", n)
	}
	feeds, cats, err := store.UnreadCountsForUser(ctx, alice.ID)
	if err != nil || len(feeds) != 4 || cats[cat.ID] != 400 {
		t.Fatalf("sidebar counts: feeds=%v cats=%v err=%v", feeds, cats, err)
	}
}
