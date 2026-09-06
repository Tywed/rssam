package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminFeedRow is a feed with health and storage stats for the admin dashboard.
type AdminFeedRow struct {
	Feed
	EntryCount   int
	UnreadCount  int
	HasQueuedJob bool
}

// AdminFeedSummary holds aggregate counters for the admin feeds dashboard.
type AdminFeedSummary struct {
	TotalFeeds   int
	OKCount      int
	ErrorCount   int
	PausedCount  int
	WaitingCount int
	TotalEntries int
	TotalUnread  int
}

// PollFeedJobCounts is a snapshot of poll_feed jobs in the queue.
type PollFeedJobCounts struct {
	Pending       int
	Running       int
	Overdue       int
	Stale         int
	MaxLagSeconds float64
}

// AdminFeedJob is the current poll_feed job for a feed, if any.
type AdminFeedJob struct {
	ID        int64
	RunAt     time.Time
	Attempts  int
	LastError *string
	LockedAt  *time.Time
}

// AdminFeedsListParams filters and paginates the admin feeds table.
type AdminFeedsListParams struct {
	Status  string
	SortKey string
	Order   string
	Limit   int
	Offset  int
}

// AdminFeedStore provides admin-only feed monitoring queries.
type AdminFeedStore interface {
	ListAdminFeeds(ctx context.Context, limit int) ([]AdminFeedRow, error)
	ListAdminFeedsPage(ctx context.Context, params AdminFeedsListParams) ([]AdminFeedRow, int, error)
	AdminFeedSummary(ctx context.Context) (AdminFeedSummary, error)
	PollFeedJobCounts(ctx context.Context) (PollFeedJobCounts, error)
	OfferedPollsPerMin(ctx context.Context) (float64, error)
	DueWebhookLogCount(ctx context.Context) (int, error)
	GetPollFeedJob(ctx context.Context, feedID int64) (*AdminFeedJob, error)
	EstimateDatabaseSize(ctx context.Context) (int64, error)
}

func (s *PostgresStore) ListAdminFeeds(ctx context.Context, limit int) ([]AdminFeedRow, error) {
	if limit <= 0 {
		limit = 10000
	}
	if limit > 10000 {
		limit = 10000
	}
	const q = `
SELECT
  f.id, f.user_id, f.feed_url, f.feed_type, f.title, f.category_id, f.interval_minutes,
  f.etag, f.last_modified, f.last_checked_at, f.last_error, f.parsing_error_count, f.poll_paused, f.manual_paused, f.store_hash_only, f.next_check_at,
  f.bridge_state, f.scraper_rules, f.rewrite_rules, f.blocked_rules, f.keep_rules,
  f.fetch_via_proxy, f.tls_insecure, f.crawler, f.user_agent, f.webhook_id, f.icon_url, f.icon_data,
  f.created_at, f.updated_at,
  COALESCE((SELECT COUNT(*)::int FROM entries e WHERE e.feed_id = f.id), 0),
  COALESCE((SELECT COUNT(*)::int FROM entries e WHERE e.feed_id = f.id AND e.status = 'unread'), 0),
  EXISTS(SELECT 1 FROM jobs j WHERE j.type = 'poll_feed' AND j.feed_id = f.id)
FROM feeds f
ORDER BY f.title ASC, f.id ASC
LIMIT $1`
	rows, err := s.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list admin feeds: %w", err)
	}
	defer rows.Close()

	out := make([]AdminFeedRow, 0, limit)
	for rows.Next() {
		var row AdminFeedRow
		if err := rows.Scan(
			&row.ID,
			&row.UserID,
			&row.FeedURL,
			&row.FeedType,
			&row.Title,
			&row.CategoryID,
			&row.IntervalMinutes,
			&row.ETag,
			&row.LastModified,
			&row.LastCheckedAt,
			&row.LastError,
			&row.ParsingErrorCount,
			&row.PollPaused,
			&row.ManualPaused,
			&row.StoreHashOnly,
			&row.NextCheckAt,
			&row.BridgeState,
			&row.ScraperRules,
			&row.RewriteRules,
			&row.BlockedRules,
			&row.KeepRules,
			&row.FetchViaProxy,
			&row.TLSInsecure,
			&row.Crawler,
			&row.UserAgent,
			&row.WebhookID,
			&row.IconURL,
			&row.IconData,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.EntryCount,
			&row.UnreadCount,
			&row.HasQueuedJob,
		); err != nil {
			return nil, fmt.Errorf("scan admin feed row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin feeds: %w", err)
	}
	return out, nil
}

const adminFeedSelectCols = `
  f.id, f.user_id, f.feed_url, f.feed_type, f.title, f.category_id, f.interval_minutes,
  f.etag, f.last_modified, f.last_checked_at, f.last_error, f.parsing_error_count, f.poll_paused, f.manual_paused, f.store_hash_only, f.next_check_at,
  f.bridge_state, f.scraper_rules, f.rewrite_rules, f.blocked_rules, f.keep_rules,
  f.fetch_via_proxy, f.tls_insecure, f.crawler, f.user_agent, f.webhook_id, f.icon_url, f.icon_data,
  f.created_at, f.updated_at,
  COALESCE((SELECT COUNT(*)::int FROM entries e WHERE e.feed_id = f.id), 0),
  COALESCE((SELECT COUNT(*)::int FROM entries e WHERE e.feed_id = f.id AND e.status = 'unread'), 0),
  EXISTS(SELECT 1 FROM jobs j WHERE j.type = 'poll_feed' AND j.feed_id = f.id)`

const adminFeedFromJoins = `
FROM feeds f`

func adminFeedsStatusWhere(status string) string {
	switch status {
	case "paused":
		return `(f.poll_paused = TRUE OR f.manual_paused = TRUE)`
	case "errors":
		return `(COALESCE(NULLIF(f.last_error, ''), '') <> '' OR f.parsing_error_count > 0)`
	case "waiting":
		return `(f.poll_paused = FALSE AND f.manual_paused = FALSE
  AND COALESCE(NULLIF(f.last_error, ''), '') = '' AND f.parsing_error_count = 0
  AND (f.next_check_at IS NULL OR f.next_check_at <= now()
       OR EXISTS(SELECT 1 FROM jobs j WHERE j.type = 'poll_feed' AND j.feed_id = f.id)))`
	case "ok":
		return `(f.poll_paused = FALSE AND f.manual_paused = FALSE
  AND COALESCE(NULLIF(f.last_error, ''), '') = '' AND f.parsing_error_count = 0
  AND f.next_check_at IS NOT NULL AND f.next_check_at > now()
  AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.type = 'poll_feed' AND j.feed_id = f.id))`
	default:
		return ""
	}
}

func adminFeedsOrderClause(sortKey, order string) string {
	dir := "ASC"
	nulls := "NULLS FIRST"
	if order == "desc" {
		dir = "DESC"
		nulls = "NULLS LAST"
	}
	pollJob := `EXISTS(SELECT 1 FROM jobs j WHERE j.type = 'poll_feed' AND j.feed_id = f.id)`
	switch sortKey {
	case "id":
		return fmt.Sprintf("f.id %s", dir)
	case "status":
		expr := fmt.Sprintf(`(
  CASE
    WHEN f.poll_paused OR f.manual_paused THEN 1
    WHEN COALESCE(NULLIF(f.last_error, ''), '') <> '' OR f.parsing_error_count > 0 THEN 2
    WHEN f.next_check_at IS NULL OR f.next_check_at <= now() OR %s THEN 3
    ELSE 4
  END)`, pollJob)
		return fmt.Sprintf("%s %s, f.id ASC", expr, dir)
	case "last_checked":
		return fmt.Sprintf("f.last_checked_at %s %s, f.id ASC", dir, nulls)
	case "next_check":
		return fmt.Sprintf("f.next_check_at %s %s, f.id ASC", dir, nulls)
	case "errors":
		return fmt.Sprintf("f.parsing_error_count %s, f.id ASC", dir)
	case "entries":
		return fmt.Sprintf("COALESCE((SELECT COUNT(*) FROM entries e WHERE e.feed_id = f.id), 0) %s, f.id ASC", dir)
	case "unread":
		return fmt.Sprintf("COALESCE((SELECT COUNT(*) FROM entries e WHERE e.feed_id = f.id AND e.status = 'unread'), 0) %s, f.id ASC", dir)
	default:
		return fmt.Sprintf("f.title %s, f.id ASC", dir)
	}
}

func scanAdminFeedRow(rows pgx.Rows) (AdminFeedRow, error) {
	var row AdminFeedRow
	err := rows.Scan(
		&row.ID,
		&row.UserID,
		&row.FeedURL,
		&row.FeedType,
		&row.Title,
		&row.CategoryID,
		&row.IntervalMinutes,
		&row.ETag,
		&row.LastModified,
		&row.LastCheckedAt,
		&row.LastError,
		&row.ParsingErrorCount,
		&row.PollPaused,
		&row.ManualPaused,
		&row.StoreHashOnly,
		&row.NextCheckAt,
		&row.BridgeState,
		&row.ScraperRules,
		&row.RewriteRules,
		&row.BlockedRules,
		&row.KeepRules,
		&row.FetchViaProxy,
		&row.TLSInsecure,
		&row.Crawler,
		&row.UserAgent,
		&row.WebhookID,
		&row.IconURL,
		&row.IconData,
		&row.CreatedAt,
		&row.UpdatedAt,
		&row.EntryCount,
		&row.UnreadCount,
		&row.HasQueuedJob,
	)
	return row, err
}

func (s *PostgresStore) ListAdminFeedsPage(ctx context.Context, params AdminFeedsListParams) ([]AdminFeedRow, int, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := max(params.Offset, 0)

	status := strings.TrimSpace(params.Status)
	if status == "" {
		status = "all"
	}
	where := adminFeedsStatusWhere(status)
	orderBy := adminFeedsOrderClause(strings.TrimSpace(params.SortKey), strings.TrimSpace(params.Order))

	countQ := `SELECT COUNT(*)::int ` + adminFeedFromJoins
	if where != "" {
		countQ += ` WHERE ` + where
	}
	var total int
	if err := s.db.QueryRow(ctx, countQ).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count admin feeds page: %w", err)
	}

	q := `SELECT` + adminFeedSelectCols + adminFeedFromJoins
	if where != "" {
		q += ` WHERE ` + where
	}
	q += ` ORDER BY ` + orderBy + ` LIMIT $1 OFFSET $2`

	rows, err := s.db.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list admin feeds page: %w", err)
	}
	defer rows.Close()

	out := make([]AdminFeedRow, 0, limit)
	for rows.Next() {
		row, err := scanAdminFeedRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan admin feed row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate admin feeds page: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) AdminFeedSummary(ctx context.Context) (AdminFeedSummary, error) {
	const q = `
SELECT
  COUNT(*)::int,
  COUNT(*) FILTER (
    WHERE poll_paused = FALSE
      AND manual_paused = FALSE
      AND COALESCE(NULLIF(last_error, ''), '') = ''
      AND parsing_error_count = 0
      AND next_check_at IS NOT NULL
      AND next_check_at > now()
      AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.type = 'poll_feed' AND j.feed_id = feeds.id)
  )::int,
  COUNT(*) FILTER (
    WHERE COALESCE(NULLIF(last_error, ''), '') <> '' OR parsing_error_count > 0
  )::int,
  COUNT(*) FILTER (WHERE poll_paused = TRUE OR manual_paused = TRUE)::int,
  COUNT(*) FILTER (
    WHERE poll_paused = FALSE
      AND manual_paused = FALSE
      AND (
        next_check_at IS NULL
        OR next_check_at <= now()
        OR EXISTS (SELECT 1 FROM jobs j WHERE j.type = 'poll_feed' AND j.feed_id = feeds.id)
      )
  )::int,
  COALESCE((SELECT GREATEST(reltuples, 0)::bigint FROM pg_class WHERE oid = 'public.entries'::regclass), 0)::int,
  COALESCE((SELECT COUNT(*)::int FROM entries WHERE status = 'unread'), 0)
FROM feeds`
	var sum AdminFeedSummary
	err := s.db.QueryRow(ctx, q).Scan(
		&sum.TotalFeeds,
		&sum.OKCount,
		&sum.ErrorCount,
		&sum.PausedCount,
		&sum.WaitingCount,
		&sum.TotalEntries,
		&sum.TotalUnread,
	)
	if err != nil {
		return AdminFeedSummary{}, fmt.Errorf("admin feed summary: %w", err)
	}
	return sum, nil
}

func (s *PostgresStore) PollFeedJobCounts(ctx context.Context) (PollFeedJobCounts, error) {
	const q = `
SELECT
  COUNT(*) FILTER (WHERE locked_at IS NULL)::int,
  COUNT(*) FILTER (WHERE locked_at IS NOT NULL)::int,
  COUNT(*) FILTER (WHERE locked_at IS NULL AND run_at <= now())::int,
  COUNT(*) FILTER (WHERE locked_at IS NOT NULL AND locked_at < now() - interval '2 minutes')::int,
  COALESCE(EXTRACT(EPOCH FROM MAX(now() - run_at) FILTER (WHERE locked_at IS NULL AND run_at <= now())), 0)
FROM jobs
WHERE type = 'poll_feed'`
	var counts PollFeedJobCounts
	if err := s.db.QueryRow(ctx, q).Scan(&counts.Pending, &counts.Running, &counts.Overdue, &counts.Stale, &counts.MaxLagSeconds); err != nil {
		return PollFeedJobCounts{}, fmt.Errorf("poll feed job counts: %w", err)
	}
	return counts, nil
}

func (s *PostgresStore) OfferedPollsPerMin(ctx context.Context) (float64, error) {
	const q = `
SELECT COALESCE(SUM(1.0 / GREATEST(interval_minutes, 1)), 0)
FROM feeds
WHERE poll_paused = FALSE AND manual_paused = FALSE`
	var offered float64
	if err := s.db.QueryRow(ctx, q).Scan(&offered); err != nil {
		return 0, fmt.Errorf("offered polls per min: %w", err)
	}
	return offered, nil
}

func (s *PostgresStore) DueWebhookLogCount(ctx context.Context) (int, error) {
	const q = `
SELECT COUNT(*)::int
FROM webhook_logs
WHERE status = 'pending'
   OR (status = 'failed' AND (next_retry_at IS NULL OR next_retry_at <= now()))`
	var n int
	if err := s.db.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("due webhook log count: %w", err)
	}
	return n, nil
}

func (s *PostgresStore) GetPollFeedJob(ctx context.Context, feedID int64) (*AdminFeedJob, error) {
	const q = `
SELECT id, run_at, attempts, last_error, locked_at
FROM jobs
WHERE type = 'poll_feed' AND feed_id = $1
LIMIT 1`
	var job AdminFeedJob
	err := s.db.QueryRow(ctx, q, feedID).Scan(&job.ID, &job.RunAt, &job.Attempts, &job.LastError, &job.LockedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get poll feed job: %w", err)
	}
	return &job, nil
}

func (s *PostgresStore) EstimateDatabaseSize(ctx context.Context) (int64, error) {
	const q = `SELECT pg_database_size(current_database())`
	var size int64
	if err := s.db.QueryRow(ctx, q).Scan(&size); err != nil {
		return 0, fmt.Errorf("estimate database size: %w", err)
	}
	return size, nil
}

var _ AdminFeedStore = (*PostgresStore)(nil)
