//go:build integration

package storage

import (
	"context"
	"strings"
	"testing"
)

// The scheduler's hot queries must stay on the partial indexes from 0029
// after 0038 dropped the older ones.
func TestIntegration_SchedulerQueriesUseIntendedIndexes(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	var n int
	if err := store.db.QueryRow(ctx, `
SELECT count(*) FROM pg_indexes
WHERE indexname IN ('jobs_run_at_idx', 'jobs_type_run_at_idx', 'idx_feeds_poll_paused_next_check', 'feeds_user_id_idx')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d redundant indexes still present", n)
	}

	tx, err := store.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	plan := func(q string) string {
		rows, err := tx.Query(ctx, "EXPLAIN (COSTS OFF) "+q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			out += line + "\n"
		}
		return out
	}
	if p := plan(`SELECT id FROM jobs WHERE run_at <= now() AND locked_at IS NULL ORDER BY run_at, id LIMIT 10`); !strings.Contains(p, "jobs_due_unlocked_idx") {
		t.Fatalf("claim plan:\n%s", p)
	}
	if p := plan(`SELECT id FROM feeds WHERE poll_paused = FALSE AND manual_paused = FALSE AND next_check_at <= now() ORDER BY next_check_at, id LIMIT 10`); !strings.Contains(p, "feeds_due_poll_idx") {
		t.Fatalf("due feeds plan:\n%s", p)
	}
	if p := plan(`SELECT id FROM feeds WHERE user_id = 1`); !strings.Contains(p, "feeds_user_id_feed_url_uidx") {
		t.Fatalf("feeds by user plan:\n%s", p)
	}
}
