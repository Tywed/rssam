package storage

import (
	"context"
	"fmt"
)

const AppSettingFeedPollDailyResetDate = "feed_poll_daily_reset_date"

// ResetFeedPollErrorsDaily clears circuit-breaker state and error backoff for automatic polling.
// manual_paused feeds are left unchanged, and so are dead feeds
// (adminFeedsDeadWhere): those keep their error and are scheduled once in
// the next 24 h, spread evenly, so a source that comes back is still
// noticed within a day. Returns how many feeds were reset and how many
// were kept dead. The feeds are made due over
// RefreshAllSpread, not on one instant: the failing feeds of a fleet hit the
// same proxies and the same hosts, and all at once they fail together — on
// the production fleet the reset second produced the day's burst of WARN
// lines.
func (s *PostgresStore) ResetFeedPollErrorsDaily(ctx context.Context) (reset, dead int64, err error) {
	const cond = `f.manual_paused = FALSE
  AND (ps.parsing_error_count > 0 OR ps.poll_paused OR COALESCE(NULLIF(ps.last_error, ''), '') <> '')`
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM feed_poll_state ps JOIN feeds f ON f.id = ps.feed_id WHERE `+cond+` AND NOT `+adminFeedsDeadWhere).Scan(&n); err != nil {
		return 0, 0, fmt.Errorf("count feeds for daily reset: %w", err)
	}
	if n > 0 {
		cmd, err := s.db.Exec(ctx, `
UPDATE feed_poll_state ps
SET parsing_error_count = 0,
    poll_paused = FALSE,
    last_error = '',
    next_check_at = now() + random() * $1::interval
FROM feeds f
WHERE f.id = ps.feed_id
  AND `+cond+`
  AND NOT `+adminFeedsDeadWhere, RefreshAllSpread(n))
		if err != nil {
			return 0, 0, fmt.Errorf("reset feed poll errors daily: %w", err)
		}
		reset = cmd.RowsAffected()
	}
	cmd, err := s.db.Exec(ctx, `
UPDATE feed_poll_state ps
SET poll_paused = FALSE,
    next_check_at = now() + random() * interval '24 hours'
FROM feeds f
WHERE f.id = ps.feed_id
  AND `+adminFeedsDeadWhere)
	if err != nil {
		return reset, 0, fmt.Errorf("schedule dead feeds daily: %w", err)
	}
	return reset, cmd.RowsAffected(), nil
}
