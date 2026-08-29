package storage

import (
	"context"
	"fmt"
	"time"
)

// AdminWebhookRow is a webhook with delivery stats for the admin dashboard.
type AdminWebhookRow struct {
	Webhook
	SentCount          int
	Sent24h            int
	DeadCount          int
	QueueDueCount      int
	RetryingCount      int
	LastSentAt         *time.Time
	LastError          string
	FeedBindingCount   int
	FilterBindingCount int
}

// AdminWebhookSummary holds aggregate counters for the webhooks dashboard.
type AdminWebhookSummary struct {
	TotalWebhooks int
	EnabledCount  int
	DisabledCount int
	Sent24h       int
	QueueDueTotal int
	DeadTotal     int
}

// WebhookBindingFeed is a feed linked to a webhook via feeds.webhook_id.
type WebhookBindingFeed struct {
	ID    int64
	Title string
}

// WebhookBindingFilter is a filter that references a webhook in filter_actions.
type WebhookBindingFilter struct {
	ID   int64
	Name string
}

// WebhookLogRow is a delivery log enriched with entry/feed/trigger context.
type WebhookLogRow struct {
	WebhookLog
	EntryTitle    string
	FeedID        int64
	FeedTitle     string
	TriggerSource string
	FilterName    string
}

// AdminWebhookStore provides admin-only webhook monitoring queries.
type AdminWebhookStore interface {
	ListAdminWebhooks(ctx context.Context, userID int64) ([]AdminWebhookRow, error)
	AdminWebhookSummary(ctx context.Context, userID int64) (AdminWebhookSummary, error)
	GetAdminWebhook(ctx context.Context, userID, webhookID int64) (AdminWebhookRow, error)
	ListWebhookBindingFeeds(ctx context.Context, userID, webhookID int64) ([]WebhookBindingFeed, error)
	ListWebhookBindingFilters(ctx context.Context, userID, webhookID int64) ([]WebhookBindingFilter, error)
	ListWebhookLogRows(ctx context.Context, webhookID int64, status string, since *time.Time, limit, offset int) ([]WebhookLogRow, int, error)
	RetryAllWebhookLogs(ctx context.Context, webhookID int64) (int64, error)
}

func (s *PostgresStore) ListAdminWebhooks(ctx context.Context, userID int64) ([]AdminWebhookRow, error) {
	const q = `
SELECT
  w.id, w.user_id, w.filter_id, w.name, w.url, w.method, w.headers, w.body_template, w.secret, w.enabled, w.on_success_entry, w.kind, w.provider_config, w.created_at, w.updated_at,
  COALESCE(stats.sent_count, 0),
  COALESCE(stats.sent_24h, 0),
  COALESCE(stats.dead_count, 0),
  COALESCE(stats.queue_due, 0),
  COALESCE(stats.retrying, 0),
  stats.last_sent_at,
  COALESCE(stats.last_error, ''),
  COALESCE(feeds.cnt, 0),
  COALESCE(filters.cnt, 0)
FROM webhooks w
LEFT JOIN LATERAL (
  SELECT
    COUNT(*) FILTER (WHERE wl.status = 'sent')::int AS sent_count,
    COUNT(*) FILTER (WHERE wl.status = 'sent' AND wl.updated_at >= now() - interval '24 hours')::int AS sent_24h,
    COUNT(*) FILTER (WHERE wl.status = 'dead')::int AS dead_count,
    COUNT(*) FILTER (
      WHERE wl.status = 'pending'
         OR (wl.status = 'failed' AND (wl.next_retry_at IS NULL OR wl.next_retry_at <= now()))
    )::int AS queue_due,
    COUNT(*) FILTER (
      WHERE wl.status = 'failed' AND wl.next_retry_at IS NOT NULL AND wl.next_retry_at > now()
    )::int AS retrying,
    MAX(wl.updated_at) FILTER (WHERE wl.status = 'sent') AS last_sent_at,
    (
      SELECT wl2.last_error
      FROM webhook_logs wl2
      WHERE wl2.webhook_id = w.id
        AND COALESCE(wl2.last_error, '') <> ''
      ORDER BY wl2.updated_at DESC, wl2.id DESC
      LIMIT 1
    ) AS last_error
  FROM webhook_logs wl
  WHERE wl.webhook_id = w.id
) stats ON TRUE
LEFT JOIN (
  SELECT webhook_id, COUNT(*)::int AS cnt
  FROM feeds
  WHERE webhook_id IS NOT NULL
  GROUP BY webhook_id
) feeds ON feeds.webhook_id = w.id
LEFT JOIN (
  SELECT fa.action_param::bigint AS webhook_id, COUNT(DISTINCT fa.filter_id)::int AS cnt
  FROM filter_actions fa
  WHERE fa.action_type = 'webhook' AND fa.action_param ~ '^[0-9]+$'
  GROUP BY fa.action_param::bigint
) filters ON filters.webhook_id = w.id
WHERE w.user_id = $1
ORDER BY w.id DESC`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list admin webhooks: %w", err)
	}
	defer rows.Close()

	out := make([]AdminWebhookRow, 0, 16)
	for rows.Next() {
		var row AdminWebhookRow
		if err := rows.Scan(
			&row.ID,
			&row.UserID,
			&row.FilterID,
			&row.Name,
			&row.URL,
			&row.Method,
			&row.Headers,
			&row.BodyTemplate,
			&row.Secret,
			&row.Enabled,
			&row.OnSuccessEntry,
			&row.Kind,
			&row.ProviderConfig,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.SentCount,
			&row.Sent24h,
			&row.DeadCount,
			&row.QueueDueCount,
			&row.RetryingCount,
			&row.LastSentAt,
			&row.LastError,
			&row.FeedBindingCount,
			&row.FilterBindingCount,
		); err != nil {
			return nil, fmt.Errorf("scan admin webhook: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin webhooks: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) AdminWebhookSummary(ctx context.Context, userID int64) (AdminWebhookSummary, error) {
	const q = `
SELECT
  COUNT(*)::int,
  COUNT(*) FILTER (WHERE enabled)::int,
  COUNT(*) FILTER (WHERE NOT enabled)::int,
  COALESCE((
    SELECT COUNT(*)::int
    FROM webhook_logs wl
    JOIN webhooks w2 ON w2.id = wl.webhook_id
    WHERE w2.user_id = $1
      AND wl.status = 'sent'
      AND wl.updated_at >= now() - interval '24 hours'
  ), 0),
  COALESCE((
    SELECT COUNT(*)::int
    FROM webhook_logs wl
    JOIN webhooks w2 ON w2.id = wl.webhook_id
    WHERE w2.user_id = $1
      AND (
        wl.status = 'pending'
        OR (wl.status = 'failed' AND (wl.next_retry_at IS NULL OR wl.next_retry_at <= now()))
      )
  ), 0),
  COALESCE((
    SELECT COUNT(*)::int
    FROM webhook_logs wl
    JOIN webhooks w2 ON w2.id = wl.webhook_id
    WHERE w2.user_id = $1 AND wl.status = 'dead'
  ), 0)
FROM webhooks
WHERE user_id = $1`
	var sum AdminWebhookSummary
	if err := s.db.QueryRow(ctx, q, userID).Scan(
		&sum.TotalWebhooks,
		&sum.EnabledCount,
		&sum.DisabledCount,
		&sum.Sent24h,
		&sum.QueueDueTotal,
		&sum.DeadTotal,
	); err != nil {
		return AdminWebhookSummary{}, fmt.Errorf("admin webhook summary: %w", err)
	}
	return sum, nil
}

func (s *PostgresStore) GetAdminWebhook(ctx context.Context, userID, webhookID int64) (AdminWebhookRow, error) {
	rows, err := s.ListAdminWebhooks(ctx, userID)
	if err != nil {
		return AdminWebhookRow{}, err
	}
	for _, row := range rows {
		if row.ID == webhookID {
			return row, nil
		}
	}
	return AdminWebhookRow{}, ErrNotFound
}

func (s *PostgresStore) ListWebhookBindingFeeds(ctx context.Context, userID, webhookID int64) ([]WebhookBindingFeed, error) {
	const q = `
SELECT id, title
FROM feeds
WHERE user_id = $1 AND webhook_id = $2
ORDER BY title ASC, id ASC`
	rows, err := s.db.Query(ctx, q, userID, webhookID)
	if err != nil {
		return nil, fmt.Errorf("list webhook binding feeds: %w", err)
	}
	defer rows.Close()

	out := make([]WebhookBindingFeed, 0, 8)
	for rows.Next() {
		var f WebhookBindingFeed
		if err := rows.Scan(&f.ID, &f.Title); err != nil {
			return nil, fmt.Errorf("scan webhook binding feed: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhook binding feeds: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) ListWebhookBindingFilters(ctx context.Context, userID, webhookID int64) ([]WebhookBindingFilter, error) {
	const q = `
SELECT DISTINCT f.id, f.name
FROM filters f
JOIN filter_actions fa ON fa.filter_id = f.id
WHERE f.user_id = $1
  AND fa.action_type = 'webhook'
  AND fa.action_param = $2::text
ORDER BY f.name ASC, f.id ASC`
	rows, err := s.db.Query(ctx, q, userID, fmt.Sprintf("%d", webhookID))
	if err != nil {
		return nil, fmt.Errorf("list webhook binding filters: %w", err)
	}
	defer rows.Close()

	out := make([]WebhookBindingFilter, 0, 8)
	for rows.Next() {
		var f WebhookBindingFilter
		if err := rows.Scan(&f.ID, &f.Name); err != nil {
			return nil, fmt.Errorf("scan webhook binding filter: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate webhook binding filters: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) ListWebhookLogRows(ctx context.Context, webhookID int64, status string, since *time.Time, limit, offset int) ([]WebhookLogRow, int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 10000 {
		limit = 10000
	}
	if offset < 0 {
		offset = 0
	}
	status = normalizeWebhookLogStatusFilter(status)

	const q = `
SELECT
  wl.id, wl.webhook_id, wl.entry_id, wl.status, wl.attempt, wl.next_retry_at,
  wl.last_status_code, wl.last_error, wl.response_snippet, wl.created_at, wl.updated_at,
  e.title,
  f.id,
  f.title,
  CASE
    WHEN EXISTS (
      SELECT 1
      FROM filter_actions fa
      JOIN filter_matches fm ON fm.filter_id = fa.filter_id AND fm.entry_id = wl.entry_id
      WHERE fa.action_type = 'webhook' AND fa.action_param = wl.webhook_id::text
    ) THEN 'filter'
    WHEN f.webhook_id = wl.webhook_id THEN 'feed'
    ELSE 'unknown'
  END,
  COALESCE((
    SELECT fl.name
    FROM filter_actions fa
    JOIN filter_matches fm ON fm.filter_id = fa.filter_id AND fm.entry_id = wl.entry_id
    JOIN filters fl ON fl.id = fa.filter_id
    WHERE fa.action_type = 'webhook' AND fa.action_param = wl.webhook_id::text
    ORDER BY fm.matched_at DESC, fm.id DESC
    LIMIT 1
  ), ''),
  count(*) OVER()
FROM webhook_logs wl
JOIN entries e ON e.id = wl.entry_id
JOIN feeds f ON f.id = e.feed_id
WHERE wl.webhook_id = $1
  AND ($2 = '' OR wl.status = $2)
  AND ($3::timestamptz IS NULL OR wl.created_at >= $3)
ORDER BY wl.created_at DESC, wl.id DESC
LIMIT $4 OFFSET $5`

	rows, err := s.db.Query(ctx, q, webhookID, status, since, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list webhook log rows: %w", err)
	}
	defer rows.Close()

	out := make([]WebhookLogRow, 0, limit)
	total := 0
	for rows.Next() {
		var row WebhookLogRow
		if err := rows.Scan(
			&row.ID,
			&row.WebhookID,
			&row.EntryID,
			&row.Status,
			&row.Attempt,
			&row.NextRetryAt,
			&row.LastStatusCode,
			&row.LastError,
			&row.ResponseSnippet,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.EntryTitle,
			&row.FeedID,
			&row.FeedTitle,
			&row.TriggerSource,
			&row.FilterName,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("scan webhook log row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate webhook log rows: %w", err)
	}
	return out, total, nil
}

func normalizeWebhookLogStatusFilter(status string) string {
	switch status {
	case "pending", "sent", "failed", "dead":
		return status
	default:
		return ""
	}
}

func (s *PostgresStore) RetryAllWebhookLogs(ctx context.Context, webhookID int64) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
UPDATE webhook_logs
SET status = 'pending',
    next_retry_at = now(),
    updated_at = now()
WHERE webhook_id = $1 AND status IN ('failed', 'dead')`, webhookID)
	if err != nil {
		return 0, fmt.Errorf("retry all webhook logs: %w", err)
	}
	return cmd.RowsAffected(), nil
}

var _ AdminWebhookStore = (*PostgresStore)(nil)
