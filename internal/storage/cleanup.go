package storage

import (
	"context"
	"fmt"
	"time"
)

const deleteBatchSize = 5000

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
	RemovedEntries int64
	WebhookLogs    int64
	FilterMatches  int64
	FeedEntries    int64
	FeedEntryDedup int64
}

func (s *PostgresStore) RunRetentionCleanup(ctx context.Context, opts RetentionCleanupOpts) (RetentionCleanupResult, error) {
	var result RetentionCleanupResult
	now := time.Now().UTC()

	n, err := s.deleteInBatches(ctx, `
DELETE FROM entries
WHERE id IN (
  SELECT id FROM entries
  WHERE status = $1 AND updated_at < $2
  LIMIT $3
)`, EntryStatusRemoved, opts.RemovedEntriesBefore.UTC(), deleteBatchSize)
	if err != nil {
		return result, fmt.Errorf("delete removed entries: %w", err)
	}
	result.RemovedEntries = n

	n, err = s.deleteInBatches(ctx, `
DELETE FROM webhook_logs
WHERE id IN (
  SELECT id FROM webhook_logs
  WHERE created_at < $1
  LIMIT $2
)`, opts.WebhookLogsBefore.UTC(), deleteBatchSize)
	if err != nil {
		return result, fmt.Errorf("delete webhook logs: %w", err)
	}
	result.WebhookLogs = n

	if !opts.FilterMatchesBefore.IsZero() {
		n, err = s.deleteInBatches(ctx, `
DELETE FROM filter_matches
WHERE id IN (
  SELECT id FROM filter_matches
  WHERE matched_at < $1
  LIMIT $2
)`, opts.FilterMatchesBefore.UTC(), deleteBatchSize)
		if err != nil {
			return result, fmt.Errorf("delete filter matches: %w", err)
		}
		result.FilterMatches = n
	}

	n, err = s.deleteInBatches(ctx, `
DELETE FROM entries
WHERE id IN (
  SELECT e.id FROM entries e
  JOIN feeds f ON e.feed_id = f.id
  WHERE f.entry_retention_days IS NOT NULL
    AND e.starred = FALSE
    AND e.created_at < $1 - (f.entry_retention_days * INTERVAL '1 day')
  LIMIT $2
)`, now, deleteBatchSize)
	if err != nil {
		return result, fmt.Errorf("delete feed entries by retention: %w", err)
	}
	result.FeedEntries = n

	n, err = s.deleteInBatches(ctx, `
DELETE FROM feed_entry_dedup
WHERE id IN (
  SELECT d.id FROM feed_entry_dedup d
  JOIN feeds f ON d.feed_id = f.id
  WHERE f.entry_retention_days IS NOT NULL
    AND d.first_seen_at < $1 - (f.entry_retention_days * INTERVAL '1 day')
  LIMIT $2
)`, now, deleteBatchSize)
	if err != nil {
		return result, fmt.Errorf("delete feed entry dedup by retention: %w", err)
	}
	result.FeedEntryDedup = n

	return result, nil
}

func (s *PostgresStore) deleteInBatches(ctx context.Context, q string, args ...any) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		cmd, err := s.db.Exec(ctx, q, args...)
		if err != nil {
			return total, err
		}
		n := cmd.RowsAffected()
		total += n
		if n < deleteBatchSize {
			return total, nil
		}
	}
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
