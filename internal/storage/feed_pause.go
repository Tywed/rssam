package storage

import (
	"context"
	"fmt"
)

// FeedPollingBlocked reports whether background polling should be skipped.
func FeedPollingBlocked(f Feed) bool {
	return f.ManualPaused || f.PollPaused
}

// SetFeedManualPaused sets or clears admin manual pause without touching circuit breaker state.
func (s *PostgresStore) SetFeedManualPaused(ctx context.Context, feedID int64, paused bool) error {
	const q = `
UPDATE feeds
SET manual_paused = $2,
    updated_at = now()
WHERE id = $1`
	cmd, err := s.db.Exec(ctx, q, feedID, paused)
	if err != nil {
		return fmt.Errorf("set feed manual pause: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
