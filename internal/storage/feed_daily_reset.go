package storage

import (
	"context"
	"fmt"
)

const AppSettingFeedPollDailyResetDate = "feed_poll_daily_reset_date"

// ResetFeedPollErrorsDaily clears circuit-breaker state and error backoff for automatic polling.
// manual_paused feeds are left unchanged. The feeds are made due over
// RefreshAllSpread, not on one instant: the failing feeds of a fleet hit the
// same proxies and the same hosts, and all at once they fail together — on
// the production fleet the reset second produced the day's burst of WARN
// lines.
func (s *PostgresStore) ResetFeedPollErrorsDaily(ctx context.Context) (int64, error) {
	const cond = `f.manual_paused = FALSE
  AND (ps.parsing_error_count > 0 OR ps.poll_paused OR COALESCE(NULLIF(ps.last_error, ''), '') <> '')`
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM feed_poll_state ps JOIN feeds f ON f.id = ps.feed_id WHERE `+cond).Scan(&n); err != nil {
		return 0, fmt.Errorf("count feeds for daily reset: %w", err)
	}
	if n == 0 {
		return 0, nil
	}
	cmd, err := s.db.Exec(ctx, `
UPDATE feed_poll_state ps
SET parsing_error_count = 0,
    poll_paused = FALSE,
    last_error = '',
    next_check_at = now() + random() * $1::interval
FROM feeds f
WHERE f.id = ps.feed_id
  AND `+cond, RefreshAllSpread(n))
	if err != nil {
		return 0, fmt.Errorf("reset feed poll errors daily: %w", err)
	}
	return cmd.RowsAffected(), nil
}
