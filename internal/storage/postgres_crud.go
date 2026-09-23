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
RETURNING id, user_id, title, color, sort_order, poll_hours, created_at, updated_at`
	var c Category
	err := s.db.QueryRow(ctx, q, userID, title, strings.TrimSpace(color)).Scan(
		&c.ID, &c.UserID, &c.Title, &c.Color, &c.SortOrder, &c.PollHours, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return Category{}, fmt.Errorf("create category: %w", err)
	}
	return c, nil
}

func (s *PostgresStore) ListCategories(ctx context.Context, userID int64, limit, offset int) ([]Category, int, error) {
	base := `
SELECT id, user_id, title, color, sort_order, poll_hours, created_at, updated_at, count(*) OVER()
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
		if err := rows.Scan(&c.ID, &c.UserID, &c.Title, &c.Color, &c.SortOrder, &c.PollHours, &c.CreatedAt, &c.UpdatedAt, &total); err != nil {
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
RETURNING id, user_id, title, color, sort_order, poll_hours, created_at, updated_at`
	var c Category
	err := s.db.QueryRow(ctx, q, id, userID, title, strings.TrimSpace(color)).Scan(
		&c.ID, &c.UserID, &c.Title, &c.Color, &c.SortOrder, &c.PollHours, &c.CreatedAt, &c.UpdatedAt,
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
	var f Feed
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		const q = `
INSERT INTO feeds(owner_id, feed_url, feed_type, title, interval_minutes, scraper_rules, rewrite_rules, blocked_rules, keep_rules, fetch_via_proxy, tls_insecure, crawler, user_agent, store_hash_only, entry_retention_days, bridge_state, adaptive_interval)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, COALESCE($16::jsonb, '{}'::jsonb), $17)
RETURNING id, created_at, updated_at`
		if err := tx.QueryRow(ctx, q,
			userID,
			params.FeedURL, feedType, params.Title, params.IntervalMinutes,
			params.ScraperRules, params.RewriteRules, params.BlockedRules, params.KeepRules,
			params.FetchViaProxy, params.TLSInsecure, params.Crawler, params.UserAgent, params.StoreHashOnly, params.EntryRetentionDays, nullableJSON(params.BridgeState), params.AdaptiveInterval,
		).Scan(&f.ID, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return err
		}
		if err := checkSubscriptionRefs(ctx, tx, userID, SubscriptionParams{CategoryID: params.CategoryID, WebhookID: params.WebhookID}); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO subscriptions(user_id, feed_id, category_id, webhook_id) VALUES ($1, $2, $3, $4)`,
			userID, f.ID, params.CategoryID, params.WebhookID)
		return err
	})
	if err != nil {
		if isDuplicateFeedURLError(err) {
			return Feed{}, ErrDuplicateFeedURL
		}
		if isForeignKeyViolation(err) {
			return Feed{}, ErrInvalidReference
		}
		return Feed{}, fmt.Errorf("create feed: %w", err)
	}
	f.OwnerID = userID
	f.FeedURL = params.FeedURL
	f.FeedType = feedType
	f.Title = params.Title
	f.CategoryID = params.CategoryID
	f.IntervalMinutes = params.IntervalMinutes
	f.ScraperRules = params.ScraperRules
	f.RewriteRules = params.RewriteRules
	f.BlockedRules = params.BlockedRules
	f.KeepRules = params.KeepRules
	f.FetchViaProxy = params.FetchViaProxy
	f.TLSInsecure = params.TLSInsecure
	f.Crawler = params.Crawler
	f.UserAgent = params.UserAgent
	f.WebhookID = params.WebhookID
	f.StoreHashOnly = params.StoreHashOnly
	f.EntryRetentionDays = params.EntryRetentionDays
	f.AdaptiveInterval = params.AdaptiveInterval
	return f, nil
}

func (s *PostgresStore) GetFeed(ctx context.Context, userID int64, id int64) (Feed, error) {
	q := `SELECT ` + feedColumns + `, f.icon_data FROM feeds f ` + subscribedJoin + ` WHERE f.id = $2`
	return s.scanFeed(s.db.QueryRow(ctx, q, userID, id))
}

func (s *PostgresStore) GetFeedByID(ctx context.Context, id int64) (Feed, error) {
	q := `SELECT ` + feedColumns + `, f.icon_data FROM feeds f ` + catalogJoin + ` WHERE f.id = $1`
	return s.scanFeed(s.db.QueryRow(ctx, q, id))
}

// feedColumns is every feeds column except icon_data, which is a blob of up
// to 512 KiB served only by the icon endpoint through GetFeed. Every list
// selects this set so that a Feed from a list is as complete as one from
// GetFeed (rules, flags, poll state, bridge state). category_id and
// webhook_id come from the subscription alias s: subscribedJoin binds it to
// $1 = user id, catalogJoin leaves both NULL.
const feedColumns = `f.id, COALESCE(f.owner_id, 0), f.feed_url, f.feed_type, f.title, s.category_id, f.interval_minutes, f.etag, f.last_modified, f.last_checked_at, f.last_error,
       f.parsing_error_count, f.poll_paused, f.manual_paused, f.store_hash_only, f.entry_retention_days, f.adaptive_interval, f.next_check_at,
       f.bridge_state, f.scraper_rules, f.rewrite_rules, f.blocked_rules, f.keep_rules, f.fetch_via_proxy, f.tls_insecure, f.crawler, f.user_agent,
       s.webhook_id, f.icon_url, f.last_entry_at, f.created_at, f.updated_at`

const subscribedJoin = `JOIN subscriptions s ON s.feed_id = f.id AND s.user_id = $1`

const catalogJoin = `LEFT JOIN (SELECT NULL::bigint AS category_id, NULL::bigint AS webhook_id) s ON TRUE`

func feedScanTargets(f *Feed) []any {
	return []any{
		&f.ID, &f.OwnerID, &f.FeedURL, &f.FeedType, &f.Title, &f.CategoryID, &f.IntervalMinutes, &f.ETag, &f.LastModified, &f.LastCheckedAt, &f.LastError,
		&f.ParsingErrorCount, &f.PollPaused, &f.ManualPaused, &f.StoreHashOnly, &f.EntryRetentionDays, &f.AdaptiveInterval, &f.NextCheckAt,
		&f.BridgeState, &f.ScraperRules, &f.RewriteRules, &f.BlockedRules, &f.KeepRules, &f.FetchViaProxy, &f.TLSInsecure, &f.Crawler, &f.UserAgent,
		&f.WebhookID, &f.IconURL, &f.LastEntryAt, &f.CreatedAt, &f.UpdatedAt,
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
	base := `SELECT ` + feedColumns + `, count(*) OVER() FROM feeds f ` + subscribedJoin + ` ORDER BY f.id DESC`
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

func (s *PostgresStore) CountOwnedFeeds(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM feeds WHERE owner_id = $1`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count owned feeds: %w", err)
	}
	return n, nil
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
		where = `s.category_id IS NULL`
	} else {
		where = `s.category_id = $2`
		args = append(args, categoryID)
	}

	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM subscriptions s WHERE s.user_id = $1 AND `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count feeds by category: %w", err)
	}

	cols := `SELECT ` + feedColumns + ` FROM feeds f ` + subscribedJoin + ` WHERE ` + where + ` ORDER BY f.title ASC, f.id ASC`

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
	rows, err := s.db.Query(ctx, `SELECT `+feedColumns+` FROM feeds f `+catalogJoin+` ORDER BY f.id ASC LIMIT $1`, limit)
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
	var f Feed
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := checkSubscriptionRefs(ctx, tx, userID, SubscriptionParams{CategoryID: params.CategoryID, WebhookID: params.WebhookID}); err != nil {
			return err
		}
		cmd, err := tx.Exec(ctx, `UPDATE subscriptions SET category_id = $3, webhook_id = $4 WHERE user_id = $1 AND feed_id = $2`,
			userID, params.ID, params.CategoryID, params.WebhookID)
		if err != nil {
			return err
		}
		if cmd.RowsAffected() == 0 {
			return ErrNotFound
		}
		const q = `
UPDATE feeds f
SET feed_url = $2, title = $3, interval_minutes = $4,
    scraper_rules = $5, rewrite_rules = $6, blocked_rules = $7, keep_rules = $8,
    fetch_via_proxy = $9, tls_insecure = $10, crawler = $11, user_agent = $12,
    store_hash_only = $13,
    entry_retention_days = $14,
    bridge_state = COALESCE($15::jsonb, bridge_state),
    adaptive_interval = $16,
    updated_at = now()
FROM s
WHERE f.id = $1
RETURNING ` + feedColumns
		row := tx.QueryRow(ctx, `WITH s AS (SELECT $17::bigint AS category_id, $18::bigint AS webhook_id) `+q,
			params.ID, params.FeedURL, params.Title, params.IntervalMinutes,
			params.ScraperRules, params.RewriteRules, params.BlockedRules, params.KeepRules,
			params.FetchViaProxy, params.TLSInsecure, params.Crawler, params.UserAgent, params.StoreHashOnly, params.EntryRetentionDays, nullableJSON(params.BridgeState), params.AdaptiveInterval,
			params.CategoryID, params.WebhookID)
		return row.Scan(feedScanTargets(&f)...)
	})
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
WHERE id = $1 AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = feeds.id AND s.user_id = $2)`
	cmd, err := s.db.Exec(ctx, q, feedID, userID, iconURL, iconData)
	if err != nil {
		return fmt.Errorf("update feed icon: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteFeed: the caller's subscription goes with their user_entries and
// entry_labels; the catalog row (entries, dedup hashes, poll log) only when
// no subscriber remains.
func (s *PostgresStore) DeleteFeed(ctx context.Context, userID int64, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM feeds f WHERE f.id = $1 AND EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id AND s.user_id = $2)`, id, userID)
	if err != nil {
		return fmt.Errorf("delete feed: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteFeedByID(ctx context.Context, id int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM feeds WHERE id = $1`, id)
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
	return strings.Contains(pgErr.ConstraintName, "feed_url")
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
