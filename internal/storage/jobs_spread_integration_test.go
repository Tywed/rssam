//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
)

// A batch enqueue spreads run_at over EnqueueSpread(n) instead of stacking
// every job on the same instant; a single feed is not delayed noticeably.
// The feeds are created already paused in one statement: the e2e package
// shares the database in CI and its scheduler would otherwise enqueue them
// first (ListFeedsDue skips manual_paused, EnqueuePollFeedJobs does not care).
func TestIntegration_EnqueuePollFeedJobsSpread(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	hash, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "spread_" + suffix, PasswordHash: hash})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })

	const n = 300
	rows, err := store.db.Query(ctx, `
INSERT INTO feeds(user_id, feed_url, feed_type, title, interval_minutes, manual_paused)
SELECT $1, 'https://example.com/spread/' || $2 || '/' || i || '.xml', 'rss', 'spread', 60, TRUE
FROM generate_series(1, $3) AS i
RETURNING id`, u.ID, suffix, n)
	if err != nil {
		t.Fatalf("create feeds: %v", err)
	}
	ids := make([]int64, 0, n)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != n {
		t.Fatalf("created %d feeds, want %d", len(ids), n)
	}

	base := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.EnqueuePollFeedJobs(ctx, ids, base); err != nil {
		t.Fatal(err)
	}
	var minAt, maxAt time.Time
	var distinct int
	if err := store.db.QueryRow(ctx, `
SELECT min(run_at), max(run_at), count(DISTINCT run_at)
FROM jobs WHERE type = 'poll_feed' AND feed_id = ANY($1)`, ids).Scan(&minAt, &maxAt, &distinct); err != nil {
		t.Fatal(err)
	}
	spread := EnqueueSpread(n)
	if minAt.Before(base) || maxAt.After(base.Add(spread)) {
		t.Fatalf("run_at range [%s, %s] outside [%s, %s]", minAt, maxAt, base, base.Add(spread))
	}
	if distinct < n/2 || maxAt.Sub(minAt) < spread/2 {
		t.Fatalf("jobs not spread: distinct=%d range=%s want ~%s", distinct, maxAt.Sub(minAt), spread)
	}

	// Re-enqueue keeps the earlier run_at: LEAST(existing, new).
	if err := store.EnqueuePollFeedJobs(ctx, ids, base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var maxAfter time.Time
	if err := store.db.QueryRow(ctx, `SELECT max(run_at) FROM jobs WHERE feed_id = ANY($1)`, ids).Scan(&maxAfter); err != nil {
		t.Fatal(err)
	}
	if !maxAfter.Equal(maxAt) {
		t.Fatalf("re-enqueue moved run_at: %s -> %s", maxAt, maxAfter)
	}
}

func TestEnqueueSpread(t *testing.T) {
	for n, want := range map[int]time.Duration{0: 0, 1: 100 * time.Millisecond, 100: 10 * time.Second, 600: time.Minute, 5000: time.Minute} {
		if got := EnqueueSpread(n); got != want {
			t.Errorf("EnqueueSpread(%d) = %s, want %s", n, got, want)
		}
	}
}
