package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	db          *pgxpool.Pool
	ftsLanguage string
}

func NewPostgresStore(db *pgxpool.Pool, ftsLanguage ...string) *PostgresStore {
	lang := DefaultFTSLanguage
	if len(ftsLanguage) > 0 {
		lang = NormalizeFTSLanguage(ftsLanguage[0])
	}
	return &PostgresStore{db: db, ftsLanguage: lang}
}

func (s *PostgresStore) CreateCategory(ctx context.Context, userID int64, title, color string) (Category, error) {
	const q = `
INSERT INTO categories(user_id, title, color, sort_order)
VALUES (
  $1, $2, $3,
  COALESCE((SELECT MAX(sort_order) + 1 FROM categories WHERE user_id = $1), 0)
)
RETURNING id, user_id, title, color, sort_order, created_at, updated_at`
	var c Category
	err := s.db.QueryRow(ctx, q, userID, title, strings.TrimSpace(color)).Scan(
		&c.ID, &c.UserID, &c.Title, &c.Color, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return Category{}, fmt.Errorf("create category: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) ListCategories(ctx context.Context, userID int64, limit, offset int) ([]Category, int, error) {
	base := `
SELECT id, user_id, title, color, sort_order, created_at, updated_at, count(*) OVER()
FROM categories
WHERE user_id = $1
ORDER BY sort_order ASC, id ASC`
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case limit <= 0 && offset <= 0:
		rows, err = s.db.Query(ctx, base, userID)
	case limit <= 0:
		rows, err = s.db.Query(ctx, base+" OFFSET $2", userID, offset)
	default:
		rows, err = s.db.Query(ctx, base+" LIMIT $2 OFFSET $3", userID, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	out := make([]Category, 0)
	if limit > 0 {
		out = make([]Category, 0, limit)
	}
	total := 0
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.Color, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan categories: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate categories: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) UpdateCategory(ctx context.Context, userID int64, id int64, title, color string) (Category, error) {
	const q = `
UPDATE categories
SET title = $3, color = $4, updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, title, color, sort_order, created_at, updated_at`
	var c Category
	err := s.db.QueryRow(ctx, q, id, userID, title, strings.TrimSpace(color)).Scan(
		&c.ID, &c.UserID, &c.Title, &c.Color, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Category{}, ErrNotFound
		}
		return Category{}, fmt.Errorf("update category: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) ReorderCategories(ctx context.Context, userID int64, ids []int64) error {
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

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reorder categories begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var owned int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM categories WHERE user_id = $1`, userID).Scan(&owned); err != nil {
		return fmt.Errorf("reorder categories count: %w", err)
	}
	if owned != len(ids) {
		return ErrInvalidReference
	}

	const checkQ = `SELECT 1 FROM categories WHERE id = $1 AND user_id = $2`
	for _, id := range ids {
		var one int
		if err := tx.QueryRow(ctx, checkQ, id, userID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrInvalidReference
			}
			return fmt.Errorf("reorder categories verify: %w", err)
		}
	}

	const updateQ = `UPDATE categories SET sort_order = $1, updated_at = now() WHERE id = $2 AND user_id = $3`
	for i, id := range ids {
		if _, err := tx.Exec(ctx, updateQ, i, id, userID); err != nil {
			return fmt.Errorf("reorder categories update: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("reorder categories commit: %w", err)
	}
	return nil
}

func (s *PostgresStore) DeleteCategory(ctx context.Context, userID int64, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM categories WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete category: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateFeed(ctx context.Context, userID int64, params CreateFeedParams) (Feed, error) {
	if err := ValidateEntryRetentionDays(params.EntryRetentionDays); err != nil {
		return Feed{}, err
	}
	feedType := strings.TrimSpace(params.FeedType)
	if feedType == "" {
		feedType = "rss"
	}
	const q = `
INSERT INTO feeds(user_id, feed_url, feed_type, title, category_id, interval_minutes, scraper_rules, rewrite_rules, blocked_rules, keep_rules, fetch_via_proxy, tls_insecure, crawler, user_agent, webhook_id, store_hash_only, entry_retention_days, bridge_state)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, COALESCE($18::jsonb, '{}'::jsonb))
RETURNING id, user_id, feed_url, feed_type, title, category_id, interval_minutes, scraper_rules, rewrite_rules, blocked_rules, keep_rules, fetch_via_proxy, tls_insecure, crawler, user_agent, webhook_id, store_hash_only, entry_retention_days, created_at, updated_at`
	var f Feed
	err := s.db.QueryRow(ctx, q,
		userID,
		params.FeedURL, feedType, params.Title, params.CategoryID, params.IntervalMinutes,
		params.ScraperRules, params.RewriteRules, params.BlockedRules, params.KeepRules,
		params.FetchViaProxy, params.TLSInsecure, params.Crawler, params.UserAgent, params.WebhookID, params.StoreHashOnly, params.EntryRetentionDays, nullableJSON(params.BridgeState),
	).Scan(
		&f.ID, &f.UserID, &f.FeedURL, &f.FeedType, &f.Title, &f.CategoryID, &f.IntervalMinutes,
		&f.ScraperRules, &f.RewriteRules, &f.BlockedRules, &f.KeepRules,
		&f.FetchViaProxy, &f.TLSInsecure, &f.Crawler, &f.UserAgent, &f.WebhookID, &f.StoreHashOnly, &f.EntryRetentionDays,
		&f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if isDuplicateFeedURLError(err) {
			return Feed{}, ErrDuplicateFeedURL
		}
		if isForeignKeyViolation(err) {
			return Feed{}, ErrInvalidReference
		}
		return Feed{}, fmt.Errorf("create feed: %w", err)
	}
	return f, nil
}

func (s *PostgresStore) GetFeed(ctx context.Context, userID int64, id int64) (Feed, error) {
	q := `SELECT ` + feedColumns + `, icon_data FROM feeds WHERE id = $1 AND user_id = $2`
	return s.scanFeed(s.db.QueryRow(ctx, q, id, userID))
}

func (s *PostgresStore) GetFeedByID(ctx context.Context, id int64) (Feed, error) {
	q := `SELECT ` + feedColumns + `, icon_data FROM feeds WHERE id = $1`
	return s.scanFeed(s.db.QueryRow(ctx, q, id))
}

// feedColumns is every feeds column except icon_data, which is a blob of up
// to 512 KiB served only by the icon endpoint through GetFeed. Every list
// selects this set so that a Feed from a list is as complete as one from
// GetFeed (rules, flags, poll state, bridge state).
const feedColumns = `id, user_id, feed_url, feed_type, title, category_id, interval_minutes, etag, last_modified, last_checked_at, last_error,
       parsing_error_count, poll_paused, manual_paused, store_hash_only, entry_retention_days, next_check_at,
       bridge_state, scraper_rules, rewrite_rules, blocked_rules, keep_rules, fetch_via_proxy, tls_insecure, crawler, user_agent,
       webhook_id, icon_url, created_at, updated_at`

func feedScanTargets(f *Feed) []any {
	return []any{
		&f.ID, &f.UserID, &f.FeedURL, &f.FeedType, &f.Title, &f.CategoryID, &f.IntervalMinutes, &f.ETag, &f.LastModified, &f.LastCheckedAt, &f.LastError,
		&f.ParsingErrorCount, &f.PollPaused, &f.ManualPaused, &f.StoreHashOnly, &f.EntryRetentionDays, &f.NextCheckAt,
		&f.BridgeState, &f.ScraperRules, &f.RewriteRules, &f.BlockedRules, &f.KeepRules, &f.FetchViaProxy, &f.TLSInsecure, &f.Crawler, &f.UserAgent,
		&f.WebhookID, &f.IconURL, &f.CreatedAt, &f.UpdatedAt,
	}
}

func scanFeedRows(rows pgx.Rows, extra ...any) ([]Feed, error) {
	defer rows.Close()
	out := make([]Feed, 0)
	for rows.Next() {
		var f Feed
		if err := rows.Scan(append(feedScanTargets(&f), extra...)...); err != nil {
			return nil, fmt.Errorf("scan feed: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feeds: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) scanFeed(row pgx.Row) (Feed, error) {
	var f Feed
	if err := row.Scan(append(feedScanTargets(&f), &f.IconData)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Feed{}, ErrNotFound
		}
		return Feed{}, fmt.Errorf("scan feed: %w", err)
	}
	return f, nil
}

func (s *PostgresStore) ListFeeds(ctx context.Context, userID int64, limit, offset int) ([]Feed, int, error) {
	base := `SELECT ` + feedColumns + `, count(*) OVER() FROM feeds WHERE user_id = $1 ORDER BY id DESC`
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case limit <= 0 && offset <= 0:
		rows, err = s.db.Query(ctx, base, userID)
	case limit <= 0:
		rows, err = s.db.Query(ctx, base+" OFFSET $2", userID, offset)
	default:
		rows, err = s.db.Query(ctx, base+" LIMIT $2 OFFSET $3", userID, limit, offset)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list feeds: %w", err)
	}
	var total int
	out, err := scanFeedRows(rows, &total)
	if err != nil {
		return nil, 0, fmt.Errorf("list feeds: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) ListFeedsByCategory(ctx context.Context, userID, categoryID int64) ([]Feed, error) {
	feeds, _, err := s.ListFeedsByCategoryPaginated(ctx, userID, categoryID, NoLimit, 0)
	return feeds, err
}

func (s *PostgresStore) ListFeedsByCategoryPaginated(ctx context.Context, userID, categoryID int64, limit, offset int) ([]Feed, int, error) {
	var (
		where string
		args  []any
	)
	args = append(args, userID)
	if categoryID == 0 {
		where = `user_id = $1 AND category_id IS NULL`
	} else {
		where = `user_id = $1 AND category_id = $2`
		args = append(args, categoryID)
	}

	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM feeds WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count feeds by category: %w", err)
	}

	cols := `SELECT ` + feedColumns + ` FROM feeds WHERE ` + where + ` ORDER BY title ASC, id ASC`

	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case limit <= 0 && offset <= 0:
		rows, err = s.db.Query(ctx, cols, args...)
	case limit <= 0:
		rows, err = s.db.Query(ctx, cols+` OFFSET $`+fmt.Sprint(len(args)+1), append(args, offset)...)
	default:
		rows, err = s.db.Query(ctx, cols+` LIMIT $`+fmt.Sprint(len(args)+1)+` OFFSET $`+fmt.Sprint(len(args)+2), append(args, limit, offset)...)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("list feeds by category: %w", err)
	}
	out, err := scanFeedRows(rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list feeds by category: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) ListAllFeeds(ctx context.Context, limit int) ([]Feed, error) {
	if limit <= 0 {
		limit = 10000
	}
	if limit > 10000 {
		limit = 10000
	}
	rows, err := s.db.Query(ctx, `SELECT `+feedColumns+` FROM feeds ORDER BY id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list all feeds: %w", err)
	}
	out, err := scanFeedRows(rows)
	if err != nil {
		return nil, fmt.Errorf("list all feeds: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) UpdateFeed(ctx context.Context, userID int64, params UpdateFeedParams) (Feed, error) {
	if err := ValidateEntryRetentionDays(params.EntryRetentionDays); err != nil {
		return Feed{}, err
	}
	const q = `
UPDATE feeds
SET feed_url = $3, title = $4, category_id = $5, interval_minutes = $6,
    scraper_rules = $7, rewrite_rules = $8, blocked_rules = $9, keep_rules = $10,
    fetch_via_proxy = $11, tls_insecure = $12, crawler = $13, user_agent = $14,
    webhook_id = $15,
    store_hash_only = $16,
    entry_retention_days = $17,
    bridge_state = COALESCE($18::jsonb, bridge_state),
    updated_at = now()
WHERE id = $1 AND user_id = $2
RETURNING id, user_id, feed_url, title, category_id, interval_minutes, scraper_rules, rewrite_rules, blocked_rules, keep_rules, fetch_via_proxy, tls_insecure, crawler, user_agent, webhook_id, store_hash_only, entry_retention_days, created_at, updated_at`
	var f Feed
	err := s.db.QueryRow(ctx, q,
		params.ID, userID, params.FeedURL, params.Title, params.CategoryID, params.IntervalMinutes,
		params.ScraperRules, params.RewriteRules, params.BlockedRules, params.KeepRules,
		params.FetchViaProxy, params.TLSInsecure, params.Crawler, params.UserAgent, params.WebhookID, params.StoreHashOnly, params.EntryRetentionDays, nullableJSON(params.BridgeState),
	).Scan(
		&f.ID, &f.UserID, &f.FeedURL, &f.Title, &f.CategoryID, &f.IntervalMinutes,
		&f.ScraperRules, &f.RewriteRules, &f.BlockedRules, &f.KeepRules,
		&f.FetchViaProxy, &f.TLSInsecure, &f.Crawler, &f.UserAgent, &f.WebhookID, &f.StoreHashOnly, &f.EntryRetentionDays,
		&f.CreatedAt, &f.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Feed{}, ErrNotFound
		}
		if isDuplicateFeedURLError(err) {
			return Feed{}, ErrDuplicateFeedURL
		}
		if isForeignKeyViolation(err) {
			return Feed{}, ErrInvalidReference
		}
		return Feed{}, fmt.Errorf("update feed: %w", err)
	}
	return f, nil
}

func (s *PostgresStore) UpdateFeedIcon(ctx context.Context, userID, feedID int64, iconURL string, iconData []byte) error {
	const q = `
UPDATE feeds
SET icon_url = $3, icon_data = $4, updated_at = now()
WHERE id = $1 AND user_id = $2`
	cmd, err := s.db.Exec(ctx, q, feedID, userID, iconURL, iconData)
	if err != nil {
		return fmt.Errorf("update feed icon: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteFeed(ctx context.Context, userID int64, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM feeds WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isDuplicateFeedURLError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "23505" {
		return false
	}
	return pgErr.ConstraintName == "feeds_feed_url_key" ||
		pgErr.ConstraintName == "feeds_user_id_feed_url_uidx" ||
		strings.Contains(pgErr.ConstraintName, "feed_url")
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23503"
}

func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
