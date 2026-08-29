package storage

import (
	"context"
	"fmt"
	"time"
)

// RecordFeedPollFailure increments consecutive error count and may pause background polling.
func (s *PostgresStore) RecordFeedPollFailure(ctx context.Context, feedID int64, errMsg string, threshold int, checkedAt time.Time) error {
	if threshold <= 0 {
		threshold = 10
	}
	const q = `
UPDATE feeds
SET parsing_error_count = parsing_error_count + 1,
    poll_paused = (parsing_error_count + 1 >= $3),
    last_error = $2,
    last_checked_at = $4,
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, feedID, errMsg, threshold, checkedAt)
	if err != nil {
		return fmt.Errorf("record feed poll failure: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetFeedPollCircuit clears consecutive errors and circuit-breaker poll_paused.
// It does not change manual_paused — use SetFeedManualPaused for admin manual pause.
func (s *PostgresStore) ResetFeedPollCircuit(ctx context.Context, feedID int64) error {
	const q = `
UPDATE feeds
SET parsing_error_count = 0,
    poll_paused = FALSE,
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, feedID)
	if err != nil {
		return fmt.Errorf("reset feed poll circuit: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
