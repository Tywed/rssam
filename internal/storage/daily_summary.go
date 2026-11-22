package storage

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// DailySummaryTypeRow is one feed_type in the daily summary: new items and
// the feeds that produced them (feed_entry_dedup), failed polls and the
// feeds that failed (feed_poll_log).
type DailySummaryTypeRow struct {
	FeedType   string
	Feeds      int
	Inserted   int
	ErrorPolls int
	ErrorFeeds int
}

// DailySummaryCounters are the cumulative PostgreSQL counters the summary
// turns into per-day deltas against yesterday's snapshot (stored in
// app_settings). PollStateUpdates is n_tup_upd of feed_poll_state: every
// poll writes that row exactly once, so the delta is the number of polls.
type DailySummaryCounters struct {
	PollStateUpdates int64 `json:"poll_state_updates"`
	WALBytes         int64 `json:"wal_bytes"`
	DBBytes          int64 `json:"db_bytes"`
	DeadEntries      int64 `json:"dead_entries"`
}

// DailySummary is what the daily message is built from.
type DailySummary struct {
	Since, Until time.Time
	ByType       []DailySummaryTypeRow
	// Deliveries over the window by webhook_logs.status (sent, failed,
	// dead, pending), judged by updated_at.
	WebhookSent, WebhookFailed, WebhookDead, WebhookPending int
	Feeds                                                   AdminFeedSummary
	DeadFeeds                                               int
	Counters                                                DailySummaryCounters
}

// CollectDailySummary runs the summary queries (six reads, each a filtered
// scan of a small table or a catalog view).
func (s *PostgresStore) CollectDailySummary(ctx context.Context, since, until time.Time, silentAfter, dormantAfter time.Duration) (DailySummary, error) {
	out := DailySummary{Since: since, Until: until}
	byType := map[string]*DailySummaryTypeRow{}
	row := func(ft string) *DailySummaryTypeRow {
		if r, ok := byType[ft]; ok {
			return r
		}
		r := &DailySummaryTypeRow{FeedType: ft}
		byType[ft] = r
		return r
	}
	// New items: first sightings in the dedup table (both store modes write
	// it). One pass over the table a day; its index is per feed.
	rows, err := s.db.Query(ctx, `
SELECT f.feed_type, COUNT(*)::int, COUNT(DISTINCT f.id)::int
FROM feed_entry_dedup d
JOIN feeds f ON f.id = d.feed_id
WHERE d.first_seen_at >= $1 AND d.first_seen_at < $2
GROUP BY f.feed_type`, since, until)
	if err != nil {
		return out, fmt.Errorf("daily summary items: %w", err)
	}
	for rows.Next() {
		var ft string
		var n, feeds int
		if err := rows.Scan(&ft, &n, &feeds); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan daily summary items: %w", err)
		}
		r := row(ft)
		r.Inserted, r.Feeds = n, feeds
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	// Failed polls: feed_poll_log keeps every failure (repeat_count for a
	// repeated one); a quiet successful poll leaves no row there.
	rows, err = s.db.Query(ctx, `
SELECT f.feed_type,
  COALESCE(SUM(l.repeat_count), 0)::int,
  COUNT(DISTINCT f.id)::int
FROM feed_poll_log l
JOIN feeds f ON f.id = l.feed_id
WHERE NOT l.ok AND l.at >= $1 AND l.at < $2
GROUP BY f.feed_type`, since, until)
	if err != nil {
		return out, fmt.Errorf("daily summary errors: %w", err)
	}
	for rows.Next() {
		var ft string
		var polls, feeds int
		if err := rows.Scan(&ft, &polls, &feeds); err != nil {
			rows.Close()
			return out, fmt.Errorf("scan daily summary errors: %w", err)
		}
		r := row(ft)
		r.ErrorPolls, r.ErrorFeeds = polls, feeds
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	for _, r := range byType {
		out.ByType = append(out.ByType, *r)
	}
	sort.Slice(out.ByType, func(i, j int) bool { return out.ByType[i].FeedType < out.ByType[j].FeedType })

	if err := s.db.QueryRow(ctx, `
SELECT COUNT(*) FILTER (WHERE status = 'sent')::int,
       COUNT(*) FILTER (WHERE status = 'failed')::int,
       COUNT(*) FILTER (WHERE status = 'dead')::int,
       COUNT(*) FILTER (WHERE status = 'pending')::int
FROM webhook_logs
WHERE updated_at >= $1 AND updated_at < $2`, since, until).Scan(&out.WebhookSent, &out.WebhookFailed, &out.WebhookDead, &out.WebhookPending); err != nil {
		return out, fmt.Errorf("daily summary webhooks: %w", err)
	}

	if out.Feeds, err = s.AdminFeedSummary(ctx, silentAfter, dormantAfter); err != nil {
		return out, err
	}
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*)::int FROM `+feedsFrom+` WHERE `+adminFeedsDeadWhere).Scan(&out.DeadFeeds); err != nil {
		return out, fmt.Errorf("daily summary dead feeds: %w", err)
	}
	out.Counters, err = s.dailySummaryCounters(ctx)
	return out, err
}

func (s *PostgresStore) dailySummaryCounters(ctx context.Context) (DailySummaryCounters, error) {
	var c DailySummaryCounters
	err := s.db.QueryRow(ctx, `
SELECT COALESCE((SELECT n_tup_upd FROM pg_stat_user_tables WHERE relid = 'feed_poll_state'::regclass), 0),
       pg_database_size(current_database()),
       COALESCE((SELECT n_dead_tup FROM pg_stat_user_tables WHERE relid = 'entries'::regclass), 0)`).
		Scan(&c.PollStateUpdates, &c.DBBytes, &c.DeadEntries)
	if err != nil {
		return c, fmt.Errorf("daily summary counters: %w", err)
	}
	// pg_stat_wal exists since PostgreSQL 14; without it the summary has
	// no WAL line.
	if err := s.db.QueryRow(ctx, `SELECT wal_bytes::bigint FROM pg_stat_wal`).Scan(&c.WALBytes); err != nil {
		c.WALBytes = 0
	}
	return c, nil
}
