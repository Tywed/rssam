package storage

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	MinFeedSearchQueryRunes = 2
	DefaultFeedSuggestLimit = 20
	MaxFeedSuggestLimit     = 50
	MaxListFeedsByIDs       = 500
)

// SearchFeedsFilter is a lightweight title/URL lookup for UI pickers.
// Query shorter than MinFeedSearchQueryRunes is ignored unless CategoryID is set
// (browse a category without dumping the full catalog).
type SearchFeedsFilter struct {
	Query      string
	CategoryID *int64 // nil = any; 0 = uncategorized
	Limit      int
}

func clampFeedSuggestLimit(n int) int {
	if n <= 0 {
		return DefaultFeedSuggestLimit
	}
	if n > MaxFeedSuggestLimit {
		return MaxFeedSuggestLimit
	}
	return n
}

func (s *PostgresStore) SearchFeeds(ctx context.Context, userID int64, filter SearchFeedsFilter) ([]Feed, error) {
	q := strings.TrimSpace(filter.Query)
	hasQuery := utf8.RuneCountInString(q) >= MinFeedSearchQueryRunes
	hasCategory := filter.CategoryID != nil
	if !hasQuery && !hasCategory {
		return nil, nil
	}

	limit := clampFeedSuggestLimit(filter.Limit)
	var (
		where []string
		args  []any
	)
	argN := 1
	where = append(where, fmt.Sprintf("user_id = $%d", argN))
	args = append(args, userID)
	argN++

	if hasQuery {
		likeArg := fmt.Sprintf("$%d", argN)
		likePattern := "%" + escapeLikePattern(q) + "%"
		where = append(where, fmt.Sprintf("(title ILIKE %s ESCAPE '\\' OR feed_url ILIKE %s ESCAPE '\\')", likeArg, likeArg))
		args = append(args, likePattern)
		argN++
	}
	if hasCategory {
		if *filter.CategoryID == 0 {
			where = append(where, "category_id IS NULL")
		} else {
			where = append(where, fmt.Sprintf("category_id = $%d", argN))
			args = append(args, *filter.CategoryID)
			argN++
		}
	}

	sql := `
SELECT id, user_id, feed_url, feed_type, title, category_id, interval_minutes, next_check_at, created_at, updated_at
FROM feeds
WHERE ` + strings.Join(where, " AND ") + `
ORDER BY title ASC, id ASC
LIMIT $` + fmt.Sprint(argN)
	args = append(args, limit)

	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search feeds: %w", err)
	}
	defer rows.Close()

	out := make([]Feed, 0, limit)
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.UserID, &f.FeedURL, &f.FeedType, &f.Title, &f.CategoryID, &f.IntervalMinutes, &f.NextCheckAt, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan search feeds: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search feeds: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) ListFeedsByIDs(ctx context.Context, userID int64, ids []int64) ([]Feed, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > MaxListFeedsByIDs {
		ids = ids[:MaxListFeedsByIDs]
	}
	const q = `
SELECT id, user_id, feed_url, feed_type, title, category_id, interval_minutes, next_check_at, created_at, updated_at
FROM feeds
WHERE user_id = $1 AND id = ANY($2)
ORDER BY title ASC, id ASC`
	rows, err := s.db.Query(ctx, q, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("list feeds by ids: %w", err)
	}
	defer rows.Close()

	byID := make(map[int64]Feed, len(ids))
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.UserID, &f.FeedURL, &f.FeedType, &f.Title, &f.CategoryID, &f.IntervalMinutes, &f.NextCheckAt, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan feeds by ids: %w", err)
		}
		byID[f.ID] = f
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feeds by ids: %w", err)
	}

	out := make([]Feed, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if f, ok := byID[id]; ok {
			out = append(out, f)
		}
	}
	return out, nil
}
