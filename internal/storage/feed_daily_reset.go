package storage

import (
	"context"
	"fmt"
)

const AppSettingFeedPollDailyResetDate = "feed_poll_daily_reset_date"

// ResetFeedPollErrorsDaily clears circuit-breaker state and error backoff for automatic polling.
// manual_paused feeds are left unchanged.
func (s *PostgresStore) ResetFeedPollErrorsDaily(ctx context.Context) (int64, error) {
	const q = `
UPDATE feed_poll_state ps
SET parsing_error_count = 0,
    poll_paused = FALSE,
    last_error = '',
    next_check_at = now()
FROM feeds f
WHERE f.id = ps.feed_id
  AND f.manual_paused = FALSE
  AND (ps.parsing_error_count > 0 OR ps.poll_paused OR COALESCE(NULLIF(ps.last_error, ''), '') <> '')`
	cmd, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("reset feed poll errors daily: %w", err)
	}
	return cmd.RowsAffected(), nil
}
