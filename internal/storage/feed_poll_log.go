package storage

import (
	"context"
	"fmt"
	"time"
)

// FeedPollLogEntry is one poll *event* of a feed: a state change or a streak
// of identical failures. Consecutive successes are not stored.
type FeedPollLogEntry struct {
	ID          int64
	FeedID      int64
	At          time.Time
	OK          bool
	Error       string
	Inserted    int
	DurationMS  int
	RepeatCount int
}

// RecordFeedPollParams describes a finished poll attempt.
type RecordFeedPollParams struct {
	FeedID   int64
	At       time.Time
	OK       bool
	Error    string
	Inserted int
	Duration time.Duration
}

// FeedPollLogStore keeps per-feed poll history. Rows are trimmed by the
// retention cleanup (FEED_POLL_LOG_RETENTION_DAYS) and capped per feed.
type FeedPollLogStore interface {
	RecordFeedPoll(ctx context.Context, params RecordFeedPollParams) error
	ListFeedPollLog(ctx context.Context, feedID int64, limit int) ([]FeedPollLogEntry, error)
}

// MaxFeedPollLogError caps the stored error text; upstream errors can embed
// whole HTML bodies.
const MaxFeedPollLogError = 2000

// MaxFeedPollLogPerFeed is how many event rows are kept per feed (matches the
// admin UI). Older rows are deleted on insert, not on a timer.
const MaxFeedPollLogPerFeed = 30

func capPollLogError(errText string) string {
	if len(errText) > MaxFeedPollLogError {
		return errText[:MaxFeedPollLogError]
	}
	return errText
}

func pollLogDurationMS(d time.Duration) int32 {
	durMS := d.Milliseconds()
	if durMS < 0 {
		durMS = 0
	}
	if durMS > int64(^uint32(0)>>1) {
		durMS = int64(^uint32(0) >> 1)
	}
	return int32(durMS)
}

// RecordFeedPoll is one statement: the latest row of the feed is read and
// then updated (same failure again: repeat_count), left alone (success
// after success — feeds.last_checked_at already moves) or followed by a new
// row (any other change), which also trims the feed to
// MaxFeedPollLogPerFeed. The common case, a healthy feed, therefore touches
// nothing: the former BEGIN / SELECT … FOR UPDATE / COMMIT round trips
// assigned a transaction id for the row lock and wrote 96 B of WAL per poll
// for a no-op. The trimming DELETE does not see the row added in the same
// statement, hence OFFSET cap-1.
func (s *PostgresStore) RecordFeedPoll(ctx context.Context, params RecordFeedPollParams) error {
	at := params.At
	if at.IsZero() {
		at = time.Now()
	}
	const q = `
WITH latest AS (
  SELECT id, ok, error FROM feed_poll_log
  WHERE feed_id = $1
  ORDER BY at DESC, id DESC
  LIMIT 1
),
coalesced AS (
  UPDATE feed_poll_log l
  SET at = $2, inserted = $5, duration_ms = $6, repeat_count = l.repeat_count + 1
  FROM latest
  WHERE l.id = latest.id AND NOT $3 AND latest.ok = $3 AND latest.error = $4
  RETURNING l.id
),
added AS (
  INSERT INTO feed_poll_log (feed_id, at, ok, error, inserted, duration_ms, repeat_count)
  SELECT $1, $2, $3, $4, $5, $6, 1
  WHERE NOT EXISTS (SELECT 1 FROM latest WHERE latest.ok = $3 AND latest.error = $4)
  RETURNING id
)
DELETE FROM feed_poll_log
WHERE EXISTS (SELECT 1 FROM added)
  AND id IN (
    SELECT id FROM feed_poll_log
    WHERE feed_id = $1
    ORDER BY at DESC, id DESC
    OFFSET $7 - 1
  )`
	if _, err := s.db.Exec(ctx, q, params.FeedID, at.UTC(), params.OK, capPollLogError(params.Error),
		params.Inserted, pollLogDurationMS(params.Duration), MaxFeedPollLogPerFeed); err != nil {
		return fmt.Errorf("record feed poll: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListFeedPollLog(ctx context.Context, feedID int64, limit int) ([]FeedPollLogEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 500 {
		limit = 500
	}
	const q = `
SELECT id, feed_id, at, ok, error, inserted, duration_ms, COALESCE(repeat_count, 1)
FROM feed_poll_log
WHERE feed_id = $1
ORDER BY at DESC, id DESC
LIMIT $2`
	rows, err := s.db.Query(ctx, q, feedID, limit)
	if err != nil {
		return nil, fmt.Errorf("list feed poll log: %w", err)
	}
	defer rows.Close()
	out := make([]FeedPollLogEntry, 0, limit)
	for rows.Next() {
		var e FeedPollLogEntry
		if err := rows.Scan(&e.ID, &e.FeedID, &e.At, &e.OK, &e.Error, &e.Inserted, &e.DurationMS, &e.RepeatCount); err != nil {
			return nil, fmt.Errorf("scan feed poll log: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed poll log: %w", err)
	}
	return out, nil
}
