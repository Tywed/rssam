package storage

import (
	"context"
	"fmt"
	"time"
)

// FeedPollLogEntry is one poll attempt of a feed (success or failure).
type FeedPollLogEntry struct {
	ID         int64
	FeedID     int64
	At         time.Time
	OK         bool
	Error      string
	Inserted   int
	DurationMS int
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
// retention cleanup (FEED_POLL_LOG_RETENTION_DAYS).
type FeedPollLogStore interface {
	RecordFeedPoll(ctx context.Context, params RecordFeedPollParams) error
	ListFeedPollLog(ctx context.Context, feedID int64, limit int) ([]FeedPollLogEntry, error)
}

// MaxFeedPollLogError caps the stored error text; upstream errors can embed
// whole HTML bodies.
const MaxFeedPollLogError = 2000

func (s *PostgresStore) RecordFeedPoll(ctx context.Context, params RecordFeedPollParams) error {
	at := params.At
	if at.IsZero() {
		at = time.Now()
	}
	errText := params.Error
	if len(errText) > MaxFeedPollLogError {
		errText = errText[:MaxFeedPollLogError]
	}
	durMS := params.Duration.Milliseconds()
	if durMS < 0 {
		durMS = 0
	}
	if durMS > int64(^uint32(0)>>1) {
		durMS = int64(^uint32(0) >> 1)
	}
	const q = `
INSERT INTO feed_poll_log (feed_id, at, ok, error, inserted, duration_ms)
VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := s.db.Exec(ctx, q, params.FeedID, at.UTC(), params.OK, errText, params.Inserted, int32(durMS)); err != nil {
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
SELECT id, feed_id, at, ok, error, inserted, duration_ms
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
		if err := rows.Scan(&e.ID, &e.FeedID, &e.At, &e.OK, &e.Error, &e.Inserted, &e.DurationMS); err != nil {
			return nil, fmt.Errorf("scan feed poll log: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate feed poll log: %w", err)
	}
	return out, nil
}
