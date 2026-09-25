package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	// FeedPollLogBefore zero value skips feed_poll_log cleanup.
	FeedPollLogBefore time.Time
	// AuditLogBefore zero value skips audit_log cleanup.
	AuditLogBefore time.Time
}

// RetentionCleanupResult reports how many rows were deleted per table.
type RetentionCleanupResult struct {
	RemovedEntries  int64
	WebhookLogs     int64
	FilterMatches   int64
	FeedPollLog     int64
	AuditLog        int64
	FeedEntries     int64
	FeedEntryDedup  int64
	ExpiredSessions int64
}

func (s *PostgresStore) RunRetentionCleanup(ctx context.Context, opts RetentionCleanupOpts) (RetentionCleanupResult, error) {
	var result RetentionCleanupResult
	now := time.Now().UTC()

	n, err := s.purgeRemovedEntries(ctx, opts.RemovedEntriesBefore.UTC())
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

	if !opts.FeedPollLogBefore.IsZero() {
		n, err = s.deleteInBatches(ctx, `
DELETE FROM feed_poll_log
WHERE id IN (
  SELECT id FROM feed_poll_log
  WHERE at < $1
  LIMIT $2
)`, opts.FeedPollLogBefore.UTC(), deleteBatchSize)
		if err != nil {
			return result, fmt.Errorf("delete feed poll log: %w", err)
		}
		result.FeedPollLog = n
	}

	if !opts.AuditLogBefore.IsZero() {
		n, err = s.deleteInBatches(ctx, `
DELETE FROM audit_log
WHERE id IN (
  SELECT id FROM audit_log
  WHERE at < $1
  LIMIT $2
)`, opts.AuditLogBefore.UTC(), deleteBatchSize)
		if err != nil {
			return result, fmt.Errorf("delete audit log: %w", err)
		}
		result.AuditLog = n
	}

	n, err = s.deleteInBatches(ctx, `
DELETE FROM entries
WHERE id IN (
  SELECT e.id FROM entries e
  JOIN feeds f ON e.feed_id = f.id
  WHERE f.entry_retention_days IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM user_entries ue WHERE ue.entry_id = e.id AND ue.starred)
    AND e.created_at < $1::timestamptz - (f.entry_retention_days * INTERVAL '1 day')
  LIMIT $2
)`, now, deleteBatchSize)
	if err != nil {
		return result, fmt.Errorf("delete feed entries by retention: %w", err)
	}
	result.FeedEntries = n

	n, err = s.deleteInBatches(ctx, `
DELETE FROM feed_entry_dedup
WHERE (feed_id, hash) IN (
  SELECT d.feed_id, d.hash FROM feed_entry_dedup d
  JOIN feeds f ON d.feed_id = f.id
  WHERE f.entry_retention_days IS NOT NULL
    AND d.first_seen_at < $1::timestamptz - (f.entry_retention_days * INTERVAL '1 day')
  LIMIT $2
)`, now, deleteBatchSize)
	if err != nil {
		return result, fmt.Errorf("delete feed entry dedup by retention: %w", err)
	}
	result.FeedEntryDedup = n

	n, err = s.DeleteExpiredSessions(ctx, now)
	if err != nil {
		return result, fmt.Errorf("delete expired sessions: %w", err)
	}
	result.ExpiredSessions = n

	return result, nil
}

// purgeRemovedEntries drops per-user removed rows older than before and the
// entries left without any reader: those every subscriber removed and those
// stripped after webhook delivery (removed_at). Each batch is one
// transaction of two statements: a single statement cannot see its own
// deletes, and the "any reader left" check written against a CTE of purged
// rows was evaluated as a nested loop over that CTE (3.7 s per batch of
// 5 000 on 200 k entries; 15 ms this way). Returns the number of entries
// deleted.
func (s *PostgresStore) purgeRemovedEntries(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		var purged int
		var deleted int64
		err := withTx(ctx, s.db, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `
DELETE FROM user_entries ue
WHERE (ue.entry_id, ue.user_id) IN (
  SELECT entry_id, user_id FROM user_entries
  WHERE status = $1 AND updated_at < $2
  LIMIT $3
)
RETURNING ue.entry_id`, EntryStatusRemoved, before, deleteBatchSize)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
			if err != nil {
				return err
			}
			purged = len(ids)
			if purged == 0 {
				return nil
			}
			cmd, err := tx.Exec(ctx, `
DELETE FROM entries e
WHERE e.id = ANY($1)
  AND NOT EXISTS (SELECT 1 FROM user_entries ue WHERE ue.entry_id = e.id)`, ids)
			if err != nil {
				return err
			}
			deleted = cmd.RowsAffected()
			return nil
		})
		if err != nil {
			return total, err
		}
		total += deleted
		if purged < deleteBatchSize {
			break
		}
	}
	n, err := s.deleteInBatches(ctx, `
DELETE FROM entries
WHERE id IN (SELECT id FROM entries WHERE removed_at < $1 LIMIT $2)`, before, deleteBatchSize)
	return total + n, err
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
