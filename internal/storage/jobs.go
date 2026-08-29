package storage

import (
	"context"
	"fmt"
	"time"
)

func (s *PostgresStore) EnqueuePollFeedJob(ctx context.Context, feedID int64, runAt time.Time) error {
	const q = `
INSERT INTO jobs(type, feed_id, run_at)
VALUES ('poll_feed', $1, $2)
ON CONFLICT (type, feed_id) WHERE (type = 'poll_feed' AND feed_id IS NOT NULL)
DO UPDATE SET run_at = LEAST(jobs.run_at, EXCLUDED.run_at),
              updated_at = now()`
	_, err := s.db.Exec(ctx, q, feedID, runAt)
	if err != nil {
		return fmt.Errorf("enqueue poll_feed job: %w", err)
	}
	return nil
}

func (s *PostgresStore) EnqueuePollFeedJobs(ctx context.Context, feedIDs []int64, runAt time.Time) error {
	if len(feedIDs) == 0 {
		return nil
	}
	const q = `
INSERT INTO jobs(type, feed_id, run_at)
SELECT 'poll_feed', x, $2
FROM unnest($1::bigint[]) AS x
ON CONFLICT (type, feed_id) WHERE (type = 'poll_feed' AND feed_id IS NOT NULL)
DO UPDATE SET run_at = LEAST(jobs.run_at, EXCLUDED.run_at),
              updated_at = now()`
	_, err := s.db.Exec(ctx, q, feedIDs, runAt)
	if err != nil {
		return fmt.Errorf("enqueue poll_feed jobs: %w", err)
	}
	return nil
}

func (s *PostgresStore) EnqueueRefreshAllPollJobs(ctx context.Context) (feeds int, queued int, err error) {
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM feeds WHERE manual_paused = FALSE`).Scan(&feeds); err != nil {
		return 0, 0, fmt.Errorf("count feeds for refresh-all: %w", err)
	}
	if _, err := s.db.Exec(ctx, `
UPDATE feeds
SET parsing_error_count = 0,
    poll_paused = FALSE,
    updated_at = now()
WHERE manual_paused = FALSE
  AND (poll_paused OR parsing_error_count > 0)`); err != nil {
		return 0, 0, fmt.Errorf("reset circuits for refresh-all: %w", err)
	}
	cmd, err := s.db.Exec(ctx, `
INSERT INTO jobs(type, feed_id, run_at)
SELECT 'poll_feed', id, now()
FROM feeds
WHERE manual_paused = FALSE
ON CONFLICT (type, feed_id) WHERE (type = 'poll_feed' AND feed_id IS NOT NULL)
DO UPDATE SET run_at = LEAST(jobs.run_at, EXCLUDED.run_at),
              updated_at = now()`)
	if err != nil {
		return 0, 0, fmt.Errorf("enqueue refresh-all poll jobs: %w", err)
	}
	return feeds, int(cmd.RowsAffected()), nil
}

func (s *PostgresStore) ClaimDueJobs(ctx context.Context, limit int, lockedBy string) ([]Job, error) {
	if limit <= 0 {
		return nil, nil
	}
	const q = `
WITH due AS (
  SELECT id
  FROM jobs
  WHERE run_at <= now()
    AND locked_at IS NULL
  ORDER BY run_at ASC, id ASC
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE jobs
SET locked_at = now(),
    locked_by = $2,
    updated_at = now()
FROM due
WHERE jobs.id = due.id
RETURNING
  jobs.id,
  jobs.type,
  jobs.feed_id,
  jobs.run_at,
  COALESCE(jobs.payload, '{}'::jsonb) AS payload,
  jobs.attempts,
  jobs.last_error,
  jobs.locked_at,
  jobs.locked_by,
  jobs.created_at,
  jobs.updated_at`
	rows, err := s.db.Query(ctx, q, limit, lockedBy)
	if err != nil {
		return nil, fmt.Errorf("claim due jobs: %w", err)
	}
	defer rows.Close()

	out := make([]Job, 0, limit)
	for rows.Next() {
		var j Job
		if err := rows.Scan(
			&j.ID,
			&j.Type,
			&j.FeedID,
			&j.RunAt,
			&j.Payload,
			&j.Attempts,
			&j.LastError,
			&j.LockedAt,
			&j.LockedBy,
			&j.CreatedAt,
			&j.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed job: %w", err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed jobs: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) CompleteJob(ctx context.Context, jobID int64, lockedBy string) (deleted bool, err error) {
	cmd, err := s.db.Exec(ctx, `DELETE FROM jobs WHERE id = $1 AND locked_by = $2`, jobID, lockedBy)
	if err != nil {
		return false, fmt.Errorf("complete job: %w", err)
	}
	return cmd.RowsAffected() > 0, nil
}

func (s *PostgresStore) ReleaseJob(ctx context.Context, jobID int64, lockedBy string) error {
	const q = `
UPDATE jobs
SET locked_at = NULL,
    locked_by = NULL,
    updated_at = now()
WHERE id = $1 AND locked_by = $2`
	_, err := s.db.Exec(ctx, q, jobID, lockedBy)
	if err != nil {
		return fmt.Errorf("release job: %w", err)
	}
	return nil
}

// ReclaimStalePollJobs unlocks poll_feed jobs left behind by dead processes, and
// own locks older than staleAfter (hung workers that ignored cancellation).
func (s *PostgresStore) ReclaimStalePollJobs(ctx context.Context, instanceID string, staleAfter time.Duration) (int64, error) {
	if staleAfter <= 0 {
		staleAfter = 2 * time.Minute
	}
	const q = `
UPDATE jobs
SET locked_at = NULL,
    locked_by = NULL,
    updated_at = now()
WHERE type = 'poll_feed'
  AND locked_at IS NOT NULL
  AND (
    locked_by IS DISTINCT FROM $1
    OR locked_at < now() - $2::interval
  )`
	cmd, err := s.db.Exec(ctx, q, instanceID, staleAfter)
	if err != nil {
		return 0, fmt.Errorf("reclaim stale poll jobs: %w", err)
	}
	return cmd.RowsAffected(), nil
}

func (s *PostgresStore) RescheduleJob(ctx context.Context, jobID int64, lockedBy string, runAt time.Time, lastError string) error {
	const q = `
UPDATE jobs
SET run_at = $3,
    attempts = attempts + 1,
    last_error = $4,
    locked_at = NULL,
    locked_by = NULL,
    updated_at = now()
WHERE id = $1 AND locked_by = $2`
	_, err := s.db.Exec(ctx, q, jobID, lockedBy, runAt, lastError)
	if err != nil {
		return fmt.Errorf("reschedule job: %w", err)
	}
	return nil
}

// Ensure PostgresStore implements interfaces at compile time.
var _ JobStore = (*PostgresStore)(nil)
