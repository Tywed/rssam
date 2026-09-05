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
		if _, err := s.lookupUserCategory(ctx, userID, categoryID); err != nil {
			return nil, 0, err
		}
	}
	if update.MoveCategory && update.MoveToCategoryID != nil && *update.MoveToCategoryID > 0 {
		if _, err := s.lookupUserCategory(ctx, userID, *update.MoveToCategoryID); err != nil {
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

	setParts := []string{"updated_at = now()"}
	args := make([]any, 0, 4)
	argN := 1

	args = append(args, userID)
	userArg := argN
	argN++

	var catArg int
	if categoryID > 0 {
		args = append(args, categoryID)
		catArg = argN
		argN++
	}

	if update.IntervalMinutes != nil {
		setParts = append(setParts, fmt.Sprintf("interval_minutes = $%d", argN))
		args = append(args, *update.IntervalMinutes)
		argN++
	}
	if update.WebhookSet {
		if update.WebhookID != nil {
			setParts = append(setParts, fmt.Sprintf("webhook_id = $%d", argN))
			args = append(args, *update.WebhookID)
			argN++
		} else {
			setParts = append(setParts, "webhook_id = NULL")
		}
	}
	if update.StoreHashOnly != nil {
		setParts = append(setParts, fmt.Sprintf("store_hash_only = $%d", argN))
		args = append(args, *update.StoreHashOnly)
		argN++
	}
	if update.ManualPaused != nil {
		setParts = append(setParts, fmt.Sprintf("manual_paused = $%d", argN))
		args = append(args, *update.ManualPaused)
		argN++
	}
	if update.MoveCategory {
		if update.MoveToCategoryID != nil {
			setParts = append(setParts, fmt.Sprintf("category_id = $%d", argN))
			args = append(args, *update.MoveToCategoryID)
		} else {
			setParts = append(setParts, "category_id = NULL")
		}
	}

	where := fmt.Sprintf("user_id = $%d", userArg)
	if categoryID == 0 {
		where += " AND category_id IS NULL"
	} else {
		where += fmt.Sprintf(" AND category_id = $%d", catArg)
	}

	q := `UPDATE feeds SET ` + strings.Join(setParts, ", ") + ` WHERE ` + where + ` RETURNING id`

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("bulk update feeds by category: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, 0, fmt.Errorf("scan feed id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return ids, len(ids), nil
}

func (s *PostgresStore) lookupUserCategory(ctx context.Context, userID, categoryID int64) (int64, error) {
	const q = `SELECT id FROM categories WHERE id = $1 AND user_id = $2`
	var id int64
	if err := s.db.QueryRow(ctx, q, categoryID, userID).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("lookup category: %w", err)
	}
	return id, nil
}
