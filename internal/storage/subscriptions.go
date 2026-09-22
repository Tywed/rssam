package storage

import (
	"context"
	"errors"
	"fmt"

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

func (s *PostgresStore) Subscribe(ctx context.Context, userID, feedID int64, params SubscriptionParams) (Subscription, error) {
	var sub Subscription
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		var err error
		sub, err = scanSubscription(tx.QueryRow(ctx, `
INSERT INTO subscriptions(user_id, feed_id, category_id, webhook_id)
VALUES ($1, $2, $3, $4)
RETURNING `+subscriptionColumns, userID, feedID, params.CategoryID, params.WebhookID))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO user_entries(user_id, entry_id, feed_id, status, starred, sort_at)
SELECT $1, e.id, e.feed_id,
       CASE WHEN row_number() OVER (ORDER BY COALESCE(e.published_at, e.created_at) DESC, e.id DESC) <= $3 THEN $4 ELSE $5 END,
       FALSE, COALESCE(e.published_at, e.created_at)
FROM entries e
WHERE e.feed_id = $2 AND e.removed_at IS NULL
ON CONFLICT (entry_id, user_id) DO NOTHING`, userID, feedID, SubscribeUnreadBackfill, EntryStatusUnread, EntryStatusRead)
		return err
	})
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
	return sub, nil
}

func (s *PostgresStore) UpdateSubscription(ctx context.Context, userID, feedID int64, params SubscriptionParams) (Subscription, error) {
	sub, err := scanSubscription(s.db.QueryRow(ctx, `
UPDATE subscriptions SET category_id = $3, webhook_id = $4
WHERE user_id = $1 AND feed_id = $2
RETURNING `+subscriptionColumns, userID, feedID, params.CategoryID, params.WebhookID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Subscription{}, err
		}
		if isForeignKeyViolation(err) {
			return Subscription{}, ErrInvalidReference
		}
		return Subscription{}, fmt.Errorf("update subscription: %w", err)
	}
	return sub, nil
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
