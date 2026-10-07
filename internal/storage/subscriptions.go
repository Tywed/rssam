package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrAlreadySubscribed = errors.New("already subscribed")

// SubscribeUnreadBackfill is how many of the feed's newest entries a new
// subscriber gets as unread; older ones are read so that subscribing to a
// feed with years of history does not produce thousands of unread items.
const SubscribeUnreadBackfill = 100

const subscriptionColumns = `user_id, feed_id, webhook_id, created_at`

func scanSubscription(row pgx.Row) (Subscription, error) {
	var sub Subscription
	if err := row.Scan(&sub.UserID, &sub.FeedID, &sub.WebhookID, &sub.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, ErrNotFound
		}
		return Subscription{}, err
	}
	return sub, nil
}

// checkSubscriptionRefs rejects a webhook that belongs to another user: the
// FK alone would let a reader route a feed into someone else's webhook.
func checkSubscriptionRefs(ctx context.Context, q querier, userID int64, params SubscriptionParams) error {
	if params.WebhookID == nil {
		return nil
	}
	var ok bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM webhooks WHERE id = $2 AND user_id = $1)`,
		userID, params.WebhookID).Scan(&ok); err != nil {
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

// subscribeTx inserts the subscription with the user's view of the feed's
// entries.
func subscribeTx(ctx context.Context, tx pgx.Tx, userID, feedID int64, params SubscriptionParams) (Subscription, error) {
	if err := checkSubscriptionRefs(ctx, tx, userID, params); err != nil {
		return Subscription{}, err
	}
	// ON CONFLICT instead of catching 23505: a failed statement would
	// abort the surrounding transaction.
	sub, err := scanSubscription(tx.QueryRow(ctx, `
INSERT INTO subscriptions(user_id, feed_id, webhook_id)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, feed_id) DO NOTHING
RETURNING `+subscriptionColumns, userID, feedID, params.WebhookID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
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
UPDATE subscriptions SET webhook_id = $3
WHERE user_id = $1 AND feed_id = $2
RETURNING `+subscriptionColumns, userID, feedID, params.WebhookID))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Subscription{}, err
		}
		return Subscription{}, fmt.Errorf("update subscription: %w", err)
	}
	return sub, nil
}

// Unsubscribe drops the caller's subscription and per-entry state. A feed
// read through a followed category cannot be dropped alone
// (ErrFollowsCategory); the catalog row stays for the editors.
func (s *PostgresStore) Unsubscribe(ctx context.Context, userID, feedID int64) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		var follows bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM feeds f JOIN category_followers cf ON cf.category_id = f.category_id
               WHERE f.id = $2 AND cf.user_id = $1)`, userID, feedID).Scan(&follows); err != nil {
			return fmt.Errorf("unsubscribe: %w", err)
		}
		if follows {
			return ErrFollowsCategory
		}
		return unsubscribeTx(ctx, tx, userID, feedID)
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

func (s *PostgresStore) ListFeedSubscriberNames(ctx context.Context, feedID int64) ([]FeedSubscriber, error) {
	rows, err := s.db.Query(ctx, `
SELECT s.user_id, u.username, s.created_at
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
		if err := rows.Scan(&fs.UserID, &fs.Username, &fs.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan feed subscriber: %w", err)
		}
		out = append(out, fs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed subscribers: %w", err)
	}
	return out, nil
}
