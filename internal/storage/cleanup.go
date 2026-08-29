package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// RetentionCutoff returns the UTC instant before which rows are eligible for deletion.
func RetentionCutoff(now time.Time, retentionDays int) time.Time {
	return now.UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
}

// RetentionCleanupOpts controls one retention cleanup run.
type RetentionCleanupOpts struct {
	RemovedEntriesBefore time.Time
	WebhookLogsBefore    time.Time
	// FilterMatchesBefore zero value skips filter_matches cleanup.
	FilterMatchesBefore time.Time
}

// RetentionCleanupResult reports how many rows were deleted per table.
type RetentionCleanupResult struct {
	RemovedEntries   int64
	WebhookLogs      int64
	FilterMatches    int64
	FeedEntries      int64
	FeedEntryDedup   int64
}

func (s *PostgresStore) RunRetentionCleanup(ctx context.Context, opts RetentionCleanupOpts) (RetentionCleanupResult, error) {
	var result RetentionCleanupResult
	now := time.Now().UTC()
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		n, err := deleteRemovedEntriesBefore(ctx, tx, opts.RemovedEntriesBefore)
		if err != nil {
			return err
		}
		result.RemovedEntries = n

		n, err = deleteWebhookLogsBefore(ctx, tx, opts.WebhookLogsBefore)
		if err != nil {
			return err
		}
		result.WebhookLogs = n

		if !opts.FilterMatchesBefore.IsZero() {
			n, err = deleteFilterMatchesBefore(ctx, tx, opts.FilterMatchesBefore)
			if err != nil {
				return err
			}
			result.FilterMatches = n
		}

		n, err = deleteFeedEntriesByRetention(ctx, tx, now)
		if err != nil {
			return err
		}
		result.FeedEntries = n

		n, err = deleteFeedEntryDedupByRetention(ctx, tx, now)
		if err != nil {
			return err
		}
		result.FeedEntryDedup = n

		return nil
	})
	if err != nil {
		return RetentionCleanupResult{}, fmt.Errorf("retention cleanup: %w", err)
	}
	return result, nil
}

func deleteRemovedEntriesBefore(ctx context.Context, tx pgx.Tx, before time.Time) (int64, error) {
	cmd, err := tx.Exec(ctx, `
DELETE FROM entries
WHERE status = $1 AND updated_at < $2`, EntryStatusRemoved, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete removed entries: %w", err)
	}
	return cmd.RowsAffected(), nil
}

func deleteWebhookLogsBefore(ctx context.Context, tx pgx.Tx, before time.Time) (int64, error) {
	cmd, err := tx.Exec(ctx, `
DELETE FROM webhook_logs
WHERE created_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete webhook logs: %w", err)
	}
	return cmd.RowsAffected(), nil
}

func deleteFilterMatchesBefore(ctx context.Context, tx pgx.Tx, before time.Time) (int64, error) {
	cmd, err := tx.Exec(ctx, `
DELETE FROM filter_matches
WHERE matched_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete filter matches: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// ValidateEntryRetentionDays checks feed retention setting (nil/0 = disabled).
func ValidateEntryRetentionDays(days *int) error {
	if days == nil || *days == 0 {
		return nil
	}
	if *days < MinFeedEntryRetentionDays || *days > MaxFeedEntryRetentionDays {
		return fmt.Errorf("entry_retention_days must be between %d and %d", MinFeedEntryRetentionDays, MaxFeedEntryRetentionDays)
	}
	return nil
}

func deleteFeedEntriesByRetention(ctx context.Context, tx pgx.Tx, now time.Time) (int64, error) {
	cmd, err := tx.Exec(ctx, `
DELETE FROM entries e
USING feeds f
WHERE e.feed_id = f.id
  AND f.entry_retention_days IS NOT NULL
  AND e.starred = FALSE
  AND e.created_at < $1 - (f.entry_retention_days * INTERVAL '1 day')`, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete feed entries by retention: %w", err)
	}
	return cmd.RowsAffected(), nil
}

func deleteFeedEntryDedupByRetention(ctx context.Context, tx pgx.Tx, now time.Time) (int64, error) {
	cmd, err := tx.Exec(ctx, `
DELETE FROM feed_entry_dedup d
USING feeds f
WHERE d.feed_id = f.id
  AND f.entry_retention_days IS NOT NULL
  AND d.first_seen_at < $1 - (f.entry_retention_days * INTERVAL '1 day')`, now.UTC())
	if err != nil {
		return 0, fmt.Errorf("delete feed entry dedup by retention: %w", err)
	}
	return cmd.RowsAffected(), nil
}
