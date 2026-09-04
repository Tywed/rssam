package storage

import (
	"context"
	"fmt"
)

// FeedCategoryCounts holds per-category feed totals for UI display.
type FeedCategoryCounts struct {
	ByCategory    map[int64]int
	Uncategorized int
	Total         int
}

func (s *PostgresStore) FeedCountsByCategory(ctx context.Context, userID int64) (FeedCategoryCounts, error) {
	const q = `
SELECT category_id, count(*)
FROM feeds
WHERE user_id = $1
GROUP BY category_id`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return FeedCategoryCounts{}, fmt.Errorf("feed counts by category: %w", err)
	}
	defer rows.Close()

	out := FeedCategoryCounts{ByCategory: make(map[int64]int)}
	for rows.Next() {
		var catID *int64
		var n int
		if err := rows.Scan(&catID, &n); err != nil {
			return FeedCategoryCounts{}, fmt.Errorf("scan feed counts: %w", err)
		}
		if catID == nil {
			out.Uncategorized = n
		} else {
			out.ByCategory[*catID] = n
		}
		out.Total += n
	}
	if err := rows.Err(); err != nil {
		return FeedCategoryCounts{}, fmt.Errorf("iterate feed counts: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) CountFeedStatuses(ctx context.Context, userID int64) (errors, inactive int, err error) {
	const q = `
SELECT
  count(*) FILTER (WHERE coalesce(last_error, '') != '' OR poll_paused OR manual_paused OR parsing_error_count > 0),
  count(*) FILTER (WHERE poll_paused OR manual_paused)
FROM feeds
WHERE user_id = $1`
	if err := s.db.QueryRow(ctx, q, userID).Scan(&errors, &inactive); err != nil {
		return 0, 0, fmt.Errorf("count feed statuses: %w", err)
	}
	return errors, inactive, nil
}

func (s *PostgresStore) ListFeedsByStatus(ctx context.Context, userID int64, status string, limit, offset int) ([]Feed, int, error) {
	var cond string
	switch status {
	case "inactive":
		cond = `poll_paused OR manual_paused`
	default:
		cond = `coalesce(last_error, '') != '' OR poll_paused OR manual_paused OR parsing_error_count > 0`
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := `
SELECT id, user_id, feed_url, feed_type, title, category_id, interval_minutes,
       last_error, parsing_error_count, poll_paused, manual_paused,
       next_check_at, created_at, updated_at, count(*) OVER()
FROM feeds
WHERE user_id = $1
  AND (` + cond + `)
ORDER BY title ASC, id ASC
LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list feeds by status: %w", err)
	}
	defer rows.Close()

	out := make([]Feed, 0, limit)
	total := 0
	for rows.Next() {
		var f Feed
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.FeedURL, &f.FeedType, &f.Title, &f.CategoryID, &f.IntervalMinutes,
			&f.LastError, &f.ParsingErrorCount, &f.PollPaused, &f.ManualPaused,
			&f.NextCheckAt, &f.CreatedAt, &f.UpdatedAt, &total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan feeds by status: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate feeds by status: %w", err)
	}
	return out, total, nil
}
