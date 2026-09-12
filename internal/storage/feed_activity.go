package storage

import (
	"context"
	"fmt"
	"time"
)

// CountFeedItemsSince returns how many items the feed delivered since the
// given time: full entries plus hash-only dedup rows. Both sides walk a
// (feed_id, …) index; 1–8 ms on a 500k-row entries table.
func (s *PostgresStore) CountFeedItemsSince(ctx context.Context, feedID int64, since time.Time) (int, error) {
	const q = `
SELECT (SELECT count(*) FROM entries WHERE feed_id = $1 AND created_at >= $2)
     + (SELECT count(*) FROM feed_entry_dedup WHERE feed_id = $1 AND first_seen_at >= $2)`
	var n int
	if err := s.db.QueryRow(ctx, q, feedID, since).Scan(&n); err != nil {
		return 0, fmt.Errorf("count feed items since: %w", err)
	}
	return n, nil
}
