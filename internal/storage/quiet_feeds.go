package storage

import (
	"context"
	"fmt"
	"time"
)

// A feed is "quieter than usual" when it has a measurable rhythm and has
// now been silent far longer than that rhythm allows. The rhythm comes from
// feed_entry_dedup.first_seen_at over the dedup retention: items that
// arrived in one poll (less than two minutes apart) are one event; the
// feed needs at least quietMinEvents such events with a median gap under
// quietMaxMedian, and the silence must exceed both three times its p90 gap
// and 24 hours — the day floor keeps weekends and nights out. On the
// owner's fleet (2 252 feeds) this named 6 feeds, all of which had indeed
// stopped posting; it is a hint for a person, not an error.
const (
	quietMinEvents = 20
	quietMaxMedian = "6 hours"
	quietMinQuiet  = "24 hours"
	quietBatchGap  = "2 minutes"
)

// quietFeedsSQL yields feed_id, median gap, p90 gap, last item time and
// event count for every quiet feed; the joins need f and ps.
var quietFeedsSQL = fmt.Sprintf(`
WITH g AS (
  SELECT feed_id, first_seen_at,
         first_seen_at - lag(first_seen_at) OVER (PARTITION BY feed_id ORDER BY first_seen_at) AS gap
  FROM feed_entry_dedup),
s AS (
  SELECT feed_id, count(*) AS n,
         percentile_disc(0.5) WITHIN GROUP (ORDER BY gap) AS med,
         percentile_disc(0.9) WITHIN GROUP (ORDER BY gap) AS p90,
         max(first_seen_at) AS last_seen
  FROM g WHERE gap > interval '%s' GROUP BY feed_id HAVING count(*) >= %d)
SELECT s.feed_id, s.med, s.p90, s.last_seen, s.n
FROM s JOIN feeds f ON f.id = s.feed_id JOIN feed_poll_state ps ON ps.feed_id = f.id
WHERE f.manual_paused = FALSE AND ps.poll_paused = FALSE AND ps.parsing_error_count = 0
  AND s.med < interval '%s'
  AND now() - s.last_seen > GREATEST(3 * s.p90, interval '%s')`,
	quietBatchGap, quietMinEvents, quietMaxMedian, quietMinQuiet)

// adminFeedsQuietWhere is the "quiet" status filter of the admin page.
var adminFeedsQuietWhere = `f.id IN (SELECT q.feed_id FROM (` + quietFeedsSQL + `) q)`

// QuietFeed is one row of the daily summary's "quieter than usual" line.
type QuietFeed struct {
	FeedID   int64
	Title    string
	FeedType string
	// MedianGap is the feed's usual interval between polls with new items;
	// QuietFor how long it has produced nothing.
	MedianGap time.Duration
	QuietFor  time.Duration
	Events    int
}

// QuietFeeds lists the quiet feeds, longest silence relative to the usual
// rhythm first. One window scan of feed_entry_dedup (~100 MB on the
// owner's instance): once a day for the summary, on demand for the admin
// filter.
func (s *PostgresStore) QuietFeeds(ctx context.Context, limit int) ([]QuietFeed, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(ctx, `
SELECT q.feed_id, f.title, f.feed_type,
       EXTRACT(EPOCH FROM q.med)::bigint, EXTRACT(EPOCH FROM now() - q.last_seen)::bigint, q.n::int
FROM (`+quietFeedsSQL+`) q JOIN feeds f ON f.id = q.feed_id
ORDER BY EXTRACT(EPOCH FROM now() - q.last_seen) / GREATEST(EXTRACT(EPOCH FROM q.p90), 1) DESC, q.feed_id
LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("quiet feeds: %w", err)
	}
	defer rows.Close()
	var out []QuietFeed
	for rows.Next() {
		var q QuietFeed
		var med, quiet int64
		if err := rows.Scan(&q.FeedID, &q.Title, &q.FeedType, &med, &quiet, &q.Events); err != nil {
			return nil, fmt.Errorf("quiet feeds: %w", err)
		}
		q.MedianGap = time.Duration(med) * time.Second
		q.QuietFor = time.Duration(quiet) * time.Second
		out = append(out, q)
	}
	return out, rows.Err()
}

// DeadFeedArchiveAfter: a dead feed (adminFeedsDeadWhere) without a
// successful poll for this long is what the "pause dead feeds" button
// stops. On the owner's fleet 23 of 27 dead feeds had been dead for over
// 90 days; the control poll costs one request a day, so this is about a
// clean dashboard, not load, and stays a manual action.
const DeadFeedArchiveAfter = 30 * 24 * time.Hour

// PauseDeadFeeds sets manual_paused on every dead feed whose last success
// (or creation) is older than olderThan and returns how many. last_error
// is kept so the row still shows why.
func (s *PostgresStore) PauseDeadFeeds(ctx context.Context, olderThan time.Duration) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
UPDATE feeds f SET manual_paused = TRUE, updated_at = now()
FROM feed_poll_state ps
WHERE ps.feed_id = f.id AND `+adminFeedsDeadWhere+`
  AND COALESCE(ps.last_success_at, f.created_at) < now() - make_interval(secs => $1::bigint)`, int64(olderThan/time.Second))
	if err != nil {
		return 0, fmt.Errorf("pause dead feeds: %w", err)
	}
	return cmd.RowsAffected(), nil
}
