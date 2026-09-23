package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrAlreadySubscribed = errors.New("already subscribed")

// SubscribeUnreadBackfill is how many of the feed's newest entries a new
// subscriber gets as unread; older ones are read so that subscribing to a
// feed with years of history does not produce thousands of unread items.
const SubscribeUnreadBackfill = 100

const subscriptionColumns = `user_id, feed_id, category_id, webhook_id, created_at`

func scanSubscription(row pgx.Row) (Subscription, error) {
	var sub Subscription
	if err := row.Scan(&sub.UserID, &sub.FeedID, &sub.CategoryID, &sub.WebhookID, &sub.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, ErrNotFound
		}
		return Subscription{}, err
	}
	return sub, nil
}

// checkSubscriptionRefs rejects a category or webhook that belongs to
// another user: the FK alone would let a reader route a feed into someone
// else's webhook.
func checkSubscriptionRefs(ctx context.Context, q querier, userID int64, params SubscriptionParams) error {
	var ok bool
	if err := q.QueryRow(ctx, `
SELECT ($2::bigint IS NULL OR EXISTS (SELECT 1 FROM categories WHERE id = $2 AND user_id = $1))
   AND ($3::bigint IS NULL OR EXISTS (SELECT 1 FROM webhooks WHERE id = $3 AND user_id = $1))`,
		userID, params.CategoryID, params.WebhookID).Scan(&ok); err != nil {
		return fmt.Errorf("check subscription refs: %w", err)
	}
	if !ok {
		return ErrInvalidReference
	}
	return nil
}

func (s *PostgresStore) Subscribe(ctx context.Context, userID, feedID int64, params SubscriptionParams) (Subscription, error) {
	var sub Subscription
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		sub, err = subscribeTx(ctx, tx, userID, feedID, params)
		return err
	})
	if err != nil {
		return Subscription{}, err
	}
	return sub, nil
}

// SubscribeByURL subscribes to the catalog feed with this URL; ErrNotFound
// when no such feed exists (the caller then creates one).
func (s *PostgresStore) SubscribeByURL(ctx context.Context, userID int64, feedURL string, params SubscriptionParams) (Feed, error) {
	var f Feed
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		var feedID int64
		if err := tx.QueryRow(ctx, `SELECT id FROM feeds WHERE feed_url = $1`, feedURL).Scan(&feedID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("subscribe by url: %w", err)
		}
		if _, err := subscribeTx(ctx, tx, userID, feedID, params); err != nil {
			return err
		}
		q := `SELECT ` + feedColumns + `, f.icon_data FROM feeds f ` + subscribedJoin + ` WHERE f.id = $2`
		var err error
		f, err = s.scanFeed(tx.QueryRow(ctx, q, userID, feedID))
		return err
	})
	if err != nil {
		return Feed{}, err
	}
	return f, nil
}

func subscribeTx(ctx context.Context, tx pgx.Tx, userID, feedID int64, params SubscriptionParams) (Subscription, error) {
	if err := checkSubscriptionRefs(ctx, tx, userID, params); err != nil {
		return Subscription{}, err
	}
	sub, err := scanSubscription(tx.QueryRow(ctx, `
INSERT INTO subscriptions(user_id, feed_id, category_id, webhook_id)
VALUES ($1, $2, $3, $4)
RETURNING `+subscriptionColumns, userID, feedID, params.CategoryID, params.WebhookID))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Subscription{}, ErrAlreadySubscribed
		}
		if isForeignKeyViolation(err) {
			return Subscription{}, ErrInvalidReference
		}
		return Subscription{}, fmt.Errorf("subscribe: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO user_entries(user_id, entry_id, feed_id, status, starred, sort_at)
SELECT $1, e.id, e.feed_id,
       CASE WHEN row_number() OVER (ORDER BY COALESCE(e.published_at, e.created_at) DESC, e.id DESC) <= $3 THEN $4 ELSE $5 END,
       FALSE, COALESCE(e.published_at, e.created_at)
FROM entries e
WHERE e.feed_id = $2 AND e.removed_at IS NULL
ON CONFLICT (entry_id, user_id) DO NOTHING`, userID, feedID, SubscribeUnreadBackfill, EntryStatusUnread, EntryStatusRead); err != nil {
		return Subscription{}, fmt.Errorf("subscribe backfill: %w", err)
	}
	return sub, nil
}

func (s *PostgresStore) UpdateSubscription(ctx context.Context, userID, feedID int64, params SubscriptionParams) (Subscription, error) {
	if err := checkSubscriptionRefs(ctx, s.db, userID, params); err != nil {
		return Subscription{}, err
	}
	sub, err := scanSubscription(s.db.QueryRow(ctx, `
UPDATE subscriptions SET category_id = $3, webhook_id = $4
WHERE user_id = $1 AND feed_id = $2
RETURNING `+subscriptionColumns, userID, feedID, params.CategoryID, params.WebhookID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Subscription{}, err
		}
		return Subscription{}, fmt.Errorf("update subscription: %w", err)
	}
	return sub, nil
}

// Unsubscribe drops the caller's subscription and per-entry state; the
// catalog row goes with it once nobody else reads the feed.
func (s *PostgresStore) Unsubscribe(ctx context.Context, userID, feedID int64) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := unsubscribeTx(ctx, tx, userID, feedID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM feeds WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = $1)`, feedID); err != nil {
			return fmt.Errorf("unsubscribe: drop orphan feed: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) ListFeedSubscribers(ctx context.Context, feedID int64) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE feed_id = $1 ORDER BY user_id`, feedID)
	if err != nil {
		return nil, fmt.Errorf("list feed subscribers: %w", err)
	}
	return scanSubscriptions(rows)
}

func (s *PostgresStore) ListSubscriptions(ctx context.Context, userID int64) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions WHERE user_id = $1 ORDER BY feed_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	return scanSubscriptions(rows)
}

func scanSubscriptions(rows pgx.Rows) ([]Subscription, error) {
	defer rows.Close()
	out := make([]Subscription, 0)
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		out = append(out, sub)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions: %w", err)
	}
	return out, nil
}

// unsubscribeTx removes the subscription with the user's per-entry state
// and their labels on the feed's entries.
func unsubscribeTx(ctx context.Context, tx pgx.Tx, userID, feedID int64) error {
	cmd, err := tx.Exec(ctx, `DELETE FROM subscriptions WHERE user_id = $1 AND feed_id = $2`, userID, feedID)
	if err != nil {
		return fmt.Errorf("unsubscribe: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM user_entries WHERE user_id = $1 AND feed_id = $2`, userID, feedID); err != nil {
		return fmt.Errorf("unsubscribe entries: %w", err)
	}
	if _, err := tx.Exec(ctx, `
DELETE FROM entry_labels el
USING labels l, entries e
WHERE el.label_id = l.id AND l.user_id = $1
  AND e.id = el.entry_id AND e.feed_id = $2`, userID, feedID); err != nil {
		return fmt.Errorf("unsubscribe labels: %w", err)
	}
	return nil
}

const catalogColumns = `f.id, f.feed_url, f.feed_type, f.title, COALESCE(f.owner_id, 0), COALESCE(u.username, ''),
       (SELECT count(*)::int FROM subscriptions s WHERE s.feed_id = f.id),
       EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id AND s.user_id = $1),
       f.last_entry_at, f.last_error, f.created_at`

func (s *PostgresStore) ListCatalog(ctx context.Context, userID int64, filter CatalogFilter) ([]CatalogFeed, int, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := []string{"TRUE"}
	args := []any{userID}
	if q := strings.TrimSpace(filter.Query); q != "" {
		args = append(args, "%"+escapeLikePattern(q)+"%")
		where = append(where, fmt.Sprintf(`(f.title ILIKE $%d ESCAPE '\' OR f.feed_url ILIKE $%d ESCAPE '\')`, len(args), len(args)))
	}
	if filter.Subscribed != nil {
		not := ""
		if !*filter.Subscribed {
			not = "NOT "
		}
		where = append(where, not+`EXISTS (SELECT 1 FROM subscriptions s WHERE s.feed_id = f.id AND s.user_id = $1)`)
	}
	args = append(args, limit, max(filter.Offset, 0))
	rows, err := s.db.Query(ctx, `
SELECT `+catalogColumns+`, count(*) OVER()
FROM feeds f
LEFT JOIN users u ON u.id = f.owner_id
WHERE `+strings.Join(where, " AND ")+`
ORDER BY lower(f.title), f.id
LIMIT $`+fmt.Sprint(len(args)-1)+` OFFSET $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list catalog: %w", err)
	}
	defer rows.Close()
	out := make([]CatalogFeed, 0, limit)
	total := 0
	for rows.Next() {
		var c CatalogFeed
		if err := rows.Scan(&c.ID, &c.FeedURL, &c.FeedType, &c.Title, &c.OwnerID, &c.OwnerName, &c.SubscriberCount, &c.Subscribed, &c.LastEntryAt, &c.LastError, &c.CreatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("scan catalog: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate catalog: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) ListFeedSubscriberNames(ctx context.Context, feedID int64) ([]FeedSubscriber, error) {
	rows, err := s.db.Query(ctx, `
SELECT s.user_id, u.username, s.category_id, s.created_at
FROM subscriptions s JOIN users u ON u.id = s.user_id
WHERE s.feed_id = $1
ORDER BY s.created_at, s.user_id`, feedID)
	if err != nil {
		return nil, fmt.Errorf("list feed subscriber names: %w", err)
	}
	defer rows.Close()
	out := make([]FeedSubscriber, 0)
	for rows.Next() {
		var fs FeedSubscriber
		if err := rows.Scan(&fs.UserID, &fs.Username, &fs.CategoryID, &fs.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan feed subscriber: %w", err)
		}
		out = append(out, fs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed subscribers: %w", err)
	}
	return out, nil
}
