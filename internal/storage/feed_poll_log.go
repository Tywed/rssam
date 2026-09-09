package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

type pollLogAction int

const (
	pollLogInsert pollLogAction = iota
	pollLogSkip
	pollLogCoalesce
)

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

// decidePollLog: consecutive successes are dropped (feeds.last_checked_at
// already moves); consecutive identical failures bump repeat_count; anything
// else is a new event row.
func decidePollLog(latest *FeedPollLogEntry, ok bool, errText string) pollLogAction {
	if latest == nil {
		return pollLogInsert
	}
	if latest.OK == ok && latest.Error == errText {
		if ok {
			return pollLogSkip
		}
		return pollLogCoalesce
	}
	return pollLogInsert
}

func (s *PostgresStore) RecordFeedPoll(ctx context.Context, params RecordFeedPollParams) error {
	at := params.At
	if at.IsZero() {
		at = time.Now()
	}
	errText := capPollLogError(params.Error)
	durMS := pollLogDurationMS(params.Duration)

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("record feed poll: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var latest FeedPollLogEntry
	err = tx.QueryRow(ctx, `
SELECT id, ok, error
FROM feed_poll_log
WHERE feed_id = $1
ORDER BY at DESC, id DESC
LIMIT 1
FOR UPDATE`, params.FeedID).Scan(&latest.ID, &latest.OK, &latest.Error)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("record feed poll: latest: %w", err)
	}
	var latestPtr *FeedPollLogEntry
	if err == nil {
		latestPtr = &latest
	}

	switch decidePollLog(latestPtr, params.OK, errText) {
	case pollLogSkip:
		return tx.Commit(ctx)
	case pollLogCoalesce:
		if _, err := tx.Exec(ctx, `
UPDATE feed_poll_log
SET at = $2, inserted = $3, duration_ms = $4, repeat_count = repeat_count + 1
WHERE id = $1`, latest.ID, at.UTC(), params.Inserted, durMS); err != nil {
			return fmt.Errorf("record feed poll: coalesce: %w", err)
		}
	default:
		if _, err := tx.Exec(ctx, `
INSERT INTO feed_poll_log (feed_id, at, ok, error, inserted, duration_ms, repeat_count)
VALUES ($1, $2, $3, $4, $5, $6, 1)`,
			params.FeedID, at.UTC(), params.OK, errText, params.Inserted, durMS); err != nil {
			return fmt.Errorf("record feed poll: insert: %w", err)
		}
		if _, err := tx.Exec(ctx, `
DELETE FROM feed_poll_log
WHERE id IN (
  SELECT id FROM feed_poll_log
  WHERE feed_id = $1
  ORDER BY at DESC, id DESC
  OFFSET $2
)`, params.FeedID, MaxFeedPollLogPerFeed); err != nil {
			return fmt.Errorf("record feed poll: trim: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("record feed poll: commit: %w", err)
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
