package storage

import (
	"context"
	"fmt"
)

// RecordFeedPollFailure persists everything a failed poll changes on the feed
// row in one UPDATE: consecutive error count (and circuit-breaker pause once
// it reaches Threshold), last error, check time, error-backoff next check and
// bridge state.
func (s *PostgresStore) RecordFeedPollFailure(ctx context.Context, params RecordFeedPollFailureParams) error {
	threshold := params.Threshold
	if threshold <= 0 {
		threshold = 10
	}
	const q = `
UPDATE feeds
SET parsing_error_count = parsing_error_count + 1,
    poll_paused = (parsing_error_count + 1 >= $3),
    last_error = $2,
    last_checked_at = $4,
    next_check_at = $5,
    bridge_state = COALESCE($6::jsonb, bridge_state),
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, params.ID, params.Error, threshold, params.CheckedAt, params.NextCheckAt, params.BridgeState)
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

// ResetErrorFeedPollCircuits clears circuit-breaker state on feeds with errors or autostop.
// Does not change manual_paused.
func (s *PostgresStore) ResetErrorFeedPollCircuits(ctx context.Context) (int64, error) {
	const q = `
UPDATE feeds
SET parsing_error_count = 0,
    poll_paused = FALSE,
    updated_at = now()
WHERE poll_paused
   OR parsing_error_count > 0
   OR coalesce(last_error, '') != ''`
	cmd, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("reset error feed poll circuits: %w", err)
	}
	return cmd.RowsAffected(), nil
}
