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
	return s.feedCountsByCategory(ctx, `
SELECT f.category_id, count(*)
FROM feeds f JOIN subscriptions s ON s.feed_id = f.id AND s.user_id = $1
GROUP BY f.category_id`, userID)
}

func (s *PostgresStore) feedCountsByCategory(ctx context.Context, q string, args ...any) (FeedCategoryCounts, error) {
	rows, err := s.db.Query(ctx, q, args...)
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
FROM feeds f
` + subscribedJoin
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
	q := `SELECT ` + feedColumns + `, count(*) OVER() FROM feeds f ` + subscribedJoin + ` WHERE (` + cond + `) ORDER BY f.title ASC, f.id ASC LIMIT $2 OFFSET $3`
	rows, err := s.db.Query(ctx, q, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list feeds by status: %w", err)
	}
	var total int
	out, err := scanFeedRows(rows, &total)
	if err != nil {
		return nil, 0, fmt.Errorf("list feeds by status: %w", err)
	}
	return out, total, nil
}
