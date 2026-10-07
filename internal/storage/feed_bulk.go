package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

func validateBulkFeedUpdate(update BulkFeedUpdate) error {
	n := 0
	if update.IntervalMinutes != nil {
		n++
		if *update.IntervalMinutes < MinFeedIntervalMinutes || *update.IntervalMinutes > MaxFeedIntervalMinutes {
			return fmt.Errorf("interval_minutes must be between %d and %d", MinFeedIntervalMinutes, MaxFeedIntervalMinutes)
		}
	}
	if update.WebhookSet {
		n++
	}
	if update.StoreHashOnly != nil {
		n++
	}
	if update.AdaptiveInterval != nil {
		n++
	}
	if update.ManualPaused != nil {
		n++
	}
	if update.MoveCategory {
		n++
	}
	if n == 0 {
		return fmt.Errorf("nothing to update")
	}
	if n > 1 {
		return fmt.Errorf("bulk feed update: only one field group allowed")
	}
	return nil
}

// BulkUpdateFeedsByCategory updates feeds in a category for userID.
// categoryID=0 targets uncategorized feeds (category_id IS NULL).
func (s *PostgresStore) BulkUpdateFeedsByCategory(ctx context.Context, userID, categoryID int64, update BulkFeedUpdate) ([]int64, int, error) {
	if err := validateBulkFeedUpdate(update); err != nil {
		return nil, 0, err
	}
	if categoryID > 0 {
		if err := s.lookupCategory(ctx, categoryID); err != nil {
			return nil, 0, err
		}
	}
	if update.MoveCategory && update.MoveToCategoryID != nil && *update.MoveToCategoryID > 0 {
		if err := s.lookupCategory(ctx, *update.MoveToCategoryID); err != nil {
			return nil, 0, err
		}
	}
	if update.WebhookSet && update.WebhookID != nil {
		const qWh = `SELECT id FROM webhooks WHERE id = $1 AND user_id = $2`
		var whID int64
		if err := s.db.QueryRow(ctx, qWh, *update.WebhookID, userID).Scan(&whID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, 0, ErrInvalidReference
			}
			return nil, 0, fmt.Errorf("lookup webhook: %w", err)
		}
	}

	feedSet := make([]string, 0, 4)
	subSet := make([]string, 0, 2)
	args := []any{userID}
	argN := 2

	catWhere := "f.category_id IS NULL"
	if categoryID > 0 {
		catWhere = "f.category_id = $2"
		args = append(args, categoryID)
		argN++
	}

	if update.IntervalMinutes != nil {
		feedSet = append(feedSet, fmt.Sprintf("interval_minutes = $%d", argN))
		args = append(args, *update.IntervalMinutes)
		argN++
	}
	if update.WebhookSet {
		if update.WebhookID != nil {
			subSet = append(subSet, fmt.Sprintf("webhook_id = $%d", argN))
			args = append(args, *update.WebhookID)
			argN++
		} else {
			subSet = append(subSet, "webhook_id = NULL")
		}
	}
	if update.StoreHashOnly != nil {
		feedSet = append(feedSet, fmt.Sprintf("store_hash_only = $%d", argN))
		args = append(args, *update.StoreHashOnly)
		argN++
	}
	if update.AdaptiveInterval != nil {
		feedSet = append(feedSet, fmt.Sprintf("adaptive_interval = $%d", argN))
		args = append(args, *update.AdaptiveInterval)
		argN++
	}
	if update.ManualPaused != nil {
		feedSet = append(feedSet, fmt.Sprintf("manual_paused = $%d", argN))
		args = append(args, *update.ManualPaused)
	}
	var moveTo *int64
	if update.MoveCategory {
		moveTo = update.MoveToCategoryID
		if moveTo != nil && *moveTo == 0 {
			moveTo = nil
		}
	}

	// The category's feeds are selected once; the catalog update (visible
	// to every subscriber) and the webhook update (this user only) both
	// derive from that set. $1 is typed in the CTE so the query is valid
	// when no per-subscription column is set.
	q := `WITH target AS (SELECT f.id AS feed_id FROM feeds f WHERE $1::bigint IS NOT NULL AND ` + catWhere + `)`
	if len(feedSet) > 0 {
		q += `, upd_feeds AS (UPDATE feeds f SET ` + strings.Join(append(feedSet, "updated_at = now()"), ", ") + ` FROM target WHERE f.id = target.feed_id)`
	}
	if len(subSet) > 0 {
		q += `, upd_subs AS (UPDATE subscriptions s SET ` + strings.Join(subSet, ", ") + ` FROM target WHERE s.user_id = $1 AND s.feed_id = target.feed_id)`
	}
	q += ` SELECT feed_id FROM target ORDER BY feed_id`

	var ids []int64
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("bulk update feeds by category: %w", err)
		}
		ids, err = pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return fmt.Errorf("scan feed id: %w", err)
		}
		if !update.MoveCategory || (moveTo != nil && categoryID == *moveTo) {
			return nil
		}
		var oldCat *int64
		if categoryID > 0 {
			oldCat = &categoryID
		}
		for _, id := range ids {
			if _, err := tx.Exec(ctx, `UPDATE feeds SET category_id = $2, updated_at = now() WHERE id = $1`, id, moveTo); err != nil {
				return fmt.Errorf("move feed: %w", err)
			}
			if err := syncCategoryFollowersTx(ctx, tx, id, oldCat, moveTo); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return ids, len(ids), nil
}

func (s *PostgresStore) lookupCategory(ctx context.Context, categoryID int64) error {
	var id int64
	if err := s.db.QueryRow(ctx, `SELECT id FROM categories WHERE id = $1`, categoryID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lookup category: %w", err)
	}
	return nil
}
