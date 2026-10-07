package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const categoryColumns = `id, title, color, sort_order, poll_hours, created_at, updated_at`

func scanCategory(row pgx.Row) (Category, error) {
	var c Category
	if err := row.Scan(&c.ID, &c.Title, &c.Color, &c.SortOrder, &c.PollHours, &c.CreatedAt, &c.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Category{}, ErrNotFound
		}
		return Category{}, err
	}
	return c, nil
}

func (s *PostgresStore) CreateCategory(ctx context.Context, title, color string) (Category, error) {
	c, err := scanCategory(s.db.QueryRow(ctx, `
INSERT INTO categories(title, color, sort_order)
VALUES ($1, $2, COALESCE((SELECT MAX(sort_order) + 1 FROM categories), 0))
RETURNING `+categoryColumns, strings.TrimSpace(title), strings.TrimSpace(color)))
	if err != nil {
		if isUniqueViolation(err) {
			return Category{}, ErrDuplicateCategory
		}
		return Category{}, fmt.Errorf("create category: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) ListCategories(ctx context.Context, limit, offset int) ([]Category, int, error) {
	base := `SELECT ` + categoryColumns + `, count(*) OVER() FROM categories ORDER BY sort_order ASC, id ASC`
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case limit <= 0 && offset <= 0:
		rows, err = s.db.Query(ctx, base)
	case limit <= 0:
		rows, err = s.db.Query(ctx, base+" OFFSET $1", offset)
	default:
		rows, err = s.db.Query(ctx, base+" LIMIT $1 OFFSET $2", limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()
	out := make([]Category, 0)
	total := 0
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Title, &c.Color, &c.SortOrder, &c.PollHours, &c.CreatedAt, &c.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan categories: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate categories: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) UpdateCategory(ctx context.Context, id int64, title, color string) (Category, error) {
	c, err := scanCategory(s.db.QueryRow(ctx, `
UPDATE categories SET title = $2, color = $3, updated_at = now()
WHERE id = $1
RETURNING `+categoryColumns, id, strings.TrimSpace(title), strings.TrimSpace(color)))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Category{}, err
		}
		if isUniqueViolation(err) {
			return Category{}, ErrDuplicateCategory
		}
		return Category{}, fmt.Errorf("update category: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) ReorderCategories(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return ErrInvalidReference
		}
		if _, dup := seen[id]; dup {
			return ErrInvalidReference
		}
		seen[id] = struct{}{}
	}
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		var total, matched int
		if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE id = ANY($1)) FROM categories`, ids).Scan(&total, &matched); err != nil {
			return fmt.Errorf("reorder categories count: %w", err)
		}
		if total != len(ids) || matched != len(ids) {
			return ErrInvalidReference
		}
		if _, err := tx.Exec(ctx, `
UPDATE categories c SET sort_order = o.ord - 1, updated_at = now()
FROM unnest($1::bigint[]) WITH ORDINALITY AS o(id, ord)
WHERE c.id = o.id`, ids); err != nil {
			return fmt.Errorf("reorder categories update: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) DeleteCategory(ctx context.Context, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM categories WHERE id = $1`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ErrCategoryNotEmpty
		}
		return fmt.Errorf("delete category: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) FollowCategory(ctx context.Context, userID, categoryID int64) (int, error) {
	n := 0
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(ctx, `
INSERT INTO category_followers(category_id, user_id)
SELECT $1, $2 WHERE EXISTS (SELECT 1 FROM categories WHERE id = $1)
ON CONFLICT DO NOTHING`, categoryID, userID)
		if err != nil {
			if isForeignKeyViolation(err) {
				return ErrInvalidReference
			}
			return fmt.Errorf("follow category: %w", err)
		}
		if cmd.RowsAffected() == 0 {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM categories WHERE id = $1)`, categoryID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return ErrNotFound
			}
		}
		n, err = subscribeCategoryFeedsTx(ctx, tx, userID, categoryID)
		return err
	})
	return n, err
}

// subscribeCategoryFeedsTx subscribes userID to the category's feeds they
// do not read yet.
func subscribeCategoryFeedsTx(ctx context.Context, tx pgx.Tx, userID, categoryID int64) (int, error) {
	rows, err := tx.Query(ctx, `
SELECT f.id FROM feeds f
WHERE f.category_id = $1 AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id AND s.user_id = $2)
ORDER BY f.id`, categoryID, userID)
	if err != nil {
		return 0, fmt.Errorf("category feeds: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, fmt.Errorf("category feeds: %w", err)
	}
	for _, id := range ids {
		if _, err := subscribeTx(ctx, tx, userID, id, SubscriptionParams{}); err != nil && !errors.Is(err, ErrAlreadySubscribed) {
			return 0, err
		}
	}
	return len(ids), nil
}

func (s *PostgresStore) UnfollowCategory(ctx context.Context, userID, categoryID int64) (int, error) {
	n := 0
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(ctx, `DELETE FROM category_followers WHERE category_id = $1 AND user_id = $2`, categoryID, userID)
		if err != nil {
			return fmt.Errorf("unfollow category: %w", err)
		}
		if cmd.RowsAffected() == 0 {
			return ErrNotFound
		}
		rows, err := tx.Query(ctx, `
SELECT s.feed_id FROM subscriptions s JOIN feeds f ON f.id = s.feed_id
WHERE s.user_id = $1 AND f.category_id = $2 ORDER BY s.feed_id`, userID, categoryID)
		if err != nil {
			return fmt.Errorf("unfollow category feeds: %w", err)
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return fmt.Errorf("unfollow category feeds: %w", err)
		}
		for _, id := range ids {
			if err := unsubscribeTx(ctx, tx, userID, id); err != nil {
				return err
			}
		}
		n = len(ids)
		return nil
	})
	return n, err
}

func (s *PostgresStore) ListFollowedCategories(ctx context.Context, userID int64) (map[int64]bool, error) {
	rows, err := s.db.Query(ctx, `SELECT category_id FROM category_followers WHERE user_id = $1`, userID)
	if err != nil {
		return nil, fmt.Errorf("followed categories: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("followed categories: %w", err)
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

func (s *PostgresStore) CategoryFollowerCounts(ctx context.Context) (map[int64]int, error) {
	return countsByKey(ctx, s.db, `SELECT category_id, count(*)::int FROM category_followers GROUP BY category_id`)
}

func (s *PostgresStore) FeedSubscriberCounts(ctx context.Context) (map[int64]int, error) {
	return countsByKey(ctx, s.db, `SELECT feed_id, count(*)::int FROM subscriptions GROUP BY feed_id`)
}

func countsByKey(ctx context.Context, q querier, sql string) (map[int64]int, error) {
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("counts: %w", err)
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var k int64
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, fmt.Errorf("scan counts: %w", err)
		}
		out[k] = n
	}
	return out, rows.Err()
}

// syncCategoryFollowersTx is run after feedID moved from oldCat to newCat:
// followers of the new category get the feed, followers of the old one
// that do not follow the new one lose it.
func syncCategoryFollowersTx(ctx context.Context, tx pgx.Tx, feedID int64, oldCat, newCat *int64) error {
	if oldCat != nil && (newCat == nil || *oldCat != *newCat) {
		rows, err := tx.Query(ctx, `
SELECT cf.user_id FROM category_followers cf
JOIN subscriptions s ON s.user_id = cf.user_id AND s.feed_id = $1
WHERE cf.category_id = $2
  AND NOT EXISTS (SELECT 1 FROM category_followers n WHERE n.user_id = cf.user_id AND n.category_id = $3)`, feedID, *oldCat, newCat)
		if err != nil {
			return fmt.Errorf("old category followers: %w", err)
		}
		users, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return fmt.Errorf("old category followers: %w", err)
		}
		for _, u := range users {
			if err := unsubscribeTx(ctx, tx, u, feedID); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
	if newCat != nil && (oldCat == nil || *oldCat != *newCat) {
		return subscribeFollowersTx(ctx, tx, feedID, *newCat)
	}
	return nil
}

// subscribeFollowersTx gives feedID to every follower of categoryID.
func subscribeFollowersTx(ctx context.Context, tx pgx.Tx, feedID, categoryID int64) error {
	rows, err := tx.Query(ctx, `
SELECT cf.user_id FROM category_followers cf
WHERE cf.category_id = $2
  AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.user_id = cf.user_id AND s.feed_id = $1)`, feedID, categoryID)
	if err != nil {
		return fmt.Errorf("category followers: %w", err)
	}
	users, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return fmt.Errorf("category followers: %w", err)
	}
	for _, u := range users {
		if _, err := subscribeTx(ctx, tx, u, feedID, SubscriptionParams{}); err != nil && !errors.Is(err, ErrAlreadySubscribed) {
			return err
		}
	}
	return nil
}
