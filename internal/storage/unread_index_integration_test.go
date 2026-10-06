//go:build integration

package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// The unread counters (per feed, per category, per user, sidebar map) read
// the partial unread index from 0053 instead of the full per-feed index.
// Enough rows across several feeds so that the planner has a real choice.
func TestIntegration_UnreadCountersUsePartialIndex(t *testing.T) {
	store := isolatedStore(t)
	ctx := context.Background()
	alice := newIntegrationUser(t, store, "ui_alice")
	cat, _ := store.CreateCategory(ctx, alice.ID, "c", "")
	var feedIDs []int64
	for i := 0; i < 4; i++ {
		f, _ := newIntegrationFeedWithEntries(t, store, alice.ID, 300)
		if _, err := store.UpdateSubscription(ctx, alice.ID, f.ID, SubscriptionParams{CategoryID: &cat.ID}); err != nil {
			t.Fatal(err)
		}
		feedIDs = append(feedIDs, f.ID)
	}
	if _, err := store.db.Exec(ctx, `UPDATE user_entries SET status = 'read' WHERE user_id = $1 AND entry_id % 3 <> 0`, alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `ANALYZE user_entries`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(ctx, `ANALYZE subscriptions`); err != nil {
		t.Fatal(err)
	}
	plan := func(q string, args ...any) string {
		rows, err := store.db.Query(ctx, "EXPLAIN (COSTS OFF) "+q, args...)
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
		"by feed":     fmt.Sprintf(`SELECT count(*) FROM user_entries WHERE user_id = %d AND feed_id = %d AND status = 'unread'`, alice.ID, feedIDs[0]),
		"by category": fmt.Sprintf(`SELECT count(*) FROM user_entries ue JOIN subscriptions s ON s.user_id = ue.user_id AND s.feed_id = ue.feed_id WHERE ue.user_id = %d AND s.category_id = %d AND ue.status = 'unread'`, alice.ID, cat.ID),
		"total":       fmt.Sprintf(`SELECT count(*) FROM user_entries WHERE user_id = %d AND status = 'unread'`, alice.ID),
		"sidebar":     fmt.Sprintf(`SELECT feed_id, count(*) FROM user_entries WHERE user_id = %d AND status = 'unread' GROUP BY feed_id`, alice.ID),
	}
	for name, q := range queries {
		if p := plan(q); !strings.Contains(p, "user_entries_user_feed_unread_idx") {
			t.Errorf("%s does not use the partial unread index:\n%s", name, p)
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
