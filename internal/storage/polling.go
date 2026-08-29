package storage

import (
	"context"
	"fmt"
)

// ListFeedsDue returns feed IDs that should be polled now and have no poll_feed job yet.
func (s *PostgresStore) ListFeedsDue(ctx context.Context, limit int) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	const q = `
SELECT f.id
FROM feeds f
LEFT JOIN jobs j ON j.type = 'poll_feed' AND j.feed_id = f.id
WHERE f.poll_paused = FALSE
  AND f.manual_paused = FALSE
  AND (f.next_check_at IS NULL OR f.next_check_at <= now())
  AND j.id IS NULL
ORDER BY COALESCE(f.next_check_at, now()) ASC, f.id ASC
LIMIT $1`
	rows, err := s.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list due feeds: %w", err)
	}
	defer rows.Close()

	out := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan due feed id: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due feeds: %w", err)
	}
	return out, nil
}
