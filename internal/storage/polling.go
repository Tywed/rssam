package storage

import (
	"context"
	"fmt"
	"time"
)

// DueFeed is a feed the dispatcher may poll now; DueAt is when it became
// due (next_check_at, or creation for a feed never polled).
type DueFeed struct {
	ID    int64
	DueAt time.Time
}

// pollableWhere selects feeds the worker polls at all: not paused, not an
// inbound feed (fed by another rssam) and subscribed by someone — a catalog
// row nobody reads is not fetched.
const pollableWhere = `f.poll_paused = FALSE
  AND f.manual_paused = FALSE
  AND f.feed_type <> 'inbound'
  AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id)`

// ListFeedsDue returns up to limit pollable feeds whose next_check_at has
// passed, oldest due first, skipping the ids in exclude (the polls already in
// flight: a feed stays due in the table until its poll writes the next
// next_check_at). There is no queue table: the single poller per database
// (TryWorkerLock) keeps the in-flight set in memory, so a poll costs the one
// feeds UPDATE that records its outcome instead of a job INSERT, a claim
// UPDATE, that UPDATE and a job DELETE in four transactions.
func (s *PostgresStore) ListFeedsDue(ctx context.Context, limit int, exclude []int64) ([]DueFeed, error) {
	if limit <= 0 {
		return nil, nil
	}
	if exclude == nil {
		exclude = []int64{}
	}
	const q = `
SELECT f.id, COALESCE(f.next_check_at, f.created_at)
FROM feeds f
WHERE ` + pollableWhere + `
  AND (f.next_check_at IS NULL OR f.next_check_at <= now())
  AND NOT (f.id = ANY($2::bigint[]))
ORDER BY COALESCE(f.next_check_at, f.created_at) ASC, f.id ASC
LIMIT $1`
	rows, err := s.db.Query(ctx, q, limit, exclude)
	if err != nil {
		return nil, fmt.Errorf("list due feeds: %w", err)
	}
	defer rows.Close()

	out := make([]DueFeed, 0, limit)
	for rows.Next() {
		var d DueFeed
		if err := rows.Scan(&d.ID, &d.DueAt); err != nil {
			return nil, fmt.Errorf("scan due feed: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due feeds: %w", err)
	}
	return out, nil
}

// NextFeedDueAt is the earliest next_check_at among pollable feeds not in
// exclude; ok=false when there is none.
func (s *PostgresStore) NextFeedDueAt(ctx context.Context, exclude []int64) (time.Time, bool, error) {
	if exclude == nil {
		exclude = []int64{}
	}
	const q = `
SELECT min(COALESCE(f.next_check_at, now()))
FROM feeds f
WHERE ` + pollableWhere + `
  AND NOT (f.id = ANY($1::bigint[]))`
	var at *time.Time
	if err := s.db.QueryRow(ctx, q, exclude).Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("next feed due: %w", err)
	}
	if at == nil {
		return time.Time{}, false, nil
	}
	return *at, true, nil
}

// RefreshAllSpread is the next_check_at jitter for n feeds made due at once
// (refresh-all, daily reset): 100 ms per feed, at most one minute, so that
// they do not all hit the pool on the same instant; the offset persists
// because the following next_check_at is computed from the actual poll time.
func RefreshAllSpread(n int) time.Duration {
	return min(time.Duration(n)*100*time.Millisecond, time.Minute)
}

// RefreshAllFeeds resets error backoff and the circuit breaker on every
// non-manually-paused feed and makes every pollable feed due now (spread
// over RefreshAllSpread). It returns how many feeds are pollable and how
// many were made due.
func (s *PostgresStore) RefreshAllFeeds(ctx context.Context) (feeds int, queued int, err error) {
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM feeds f WHERE f.manual_paused = FALSE AND f.feed_type <> 'inbound' AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id)`).Scan(&feeds); err != nil {
		return 0, 0, fmt.Errorf("count feeds for refresh-all: %w", err)
	}
	cmd, err := s.db.Exec(ctx, `
UPDATE feeds f
SET parsing_error_count = 0,
    poll_paused = FALSE,
    next_check_at = now() + random() * $1::interval,
    updated_at = now()
WHERE f.manual_paused = FALSE
  AND f.feed_type <> 'inbound'
  AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id)`, RefreshAllSpread(feeds))
	if err != nil {
		return 0, 0, fmt.Errorf("refresh-all feeds: %w", err)
	}
	return feeds, int(cmd.RowsAffected()), nil
}
