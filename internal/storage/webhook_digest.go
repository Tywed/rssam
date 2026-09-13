package storage

import (
	"context"
	"fmt"
	"time"
)

// DigestWindowOrigin is the point digest windows are counted from: local
// midnight of the given day, so a 24 h digest goes out at midnight and a 60
// min one on the hour, in the process time zone.
func DigestWindowOrigin(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

// ClaimWebhookDigestPeers claims the other due rows of one digest webhook so
// they travel in the same message as the rows already claimed (excluded by
// id). Same parking as ClaimDueWebhookLogs.
func (s *PostgresStore) ClaimWebhookDigestPeers(ctx context.Context, webhookID int64, excludeIDs []int64, limit int) ([]WebhookLog, error) {
	if limit <= 0 {
		return nil, nil
	}
	const q = `
WITH due AS (
  SELECT id
  FROM webhook_logs
  WHERE webhook_id = $1
    AND status IN ('pending','failed')
    AND next_retry_at <= now()
    AND NOT (id = ANY($2::bigint[]))
  ORDER BY id ASC
  LIMIT $3
  FOR UPDATE SKIP LOCKED
)
UPDATE webhook_logs
SET next_retry_at = now() + interval '30 seconds',
    updated_at = now()
FROM due
WHERE webhook_logs.id = due.id
RETURNING ` + claimedWebhookLogCols
	rows, err := s.db.Query(ctx, q, webhookID, excludeIDs, limit)
	if err != nil {
		return nil, fmt.Errorf("claim webhook digest peers: %w", err)
	}
	defer rows.Close()
	return scanClaimedWebhookLogs(rows, limit, false)
}

// WebhookDigestItem is one entry of a digest message.
type WebhookDigestItem struct {
	LogID int64
	Entry Entry
	Feed  WebhookFeed
}

// LoadWebhookDigestContext loads the webhook and the entries behind the given
// log rows (one query). Rows whose entry vanished are simply absent.
func (s *PostgresStore) LoadWebhookDigestContext(ctx context.Context, webhookID int64, logIDs []int64) (Webhook, []WebhookDigestItem, error) {
	var w Webhook
	if err := s.db.QueryRow(ctx, `SELECT `+webhookSQLColumns+` FROM webhooks WHERE id = $1`, webhookID).Scan(webhookScanDest(&w)...); err != nil {
		return Webhook{}, nil, fmt.Errorf("load digest webhook: %w", err)
	}
	const q = `
SELECT wl.id, e.id, e.feed_id, e.title, e.url, e.author, e.published_at, e.created_at, f.id, f.title
FROM webhook_logs wl
JOIN entries e ON e.id = wl.entry_id
JOIN feeds f ON f.id = e.feed_id
WHERE wl.id = ANY($1::bigint[])
ORDER BY f.title, e.id`
	rows, err := s.db.Query(ctx, q, logIDs)
	if err != nil {
		return Webhook{}, nil, fmt.Errorf("load digest items: %w", err)
	}
	defer rows.Close()
	items := make([]WebhookDigestItem, 0, len(logIDs))
	for rows.Next() {
		var it WebhookDigestItem
		if err := rows.Scan(&it.LogID, &it.Entry.ID, &it.Entry.FeedID, &it.Entry.Title, &it.Entry.URL, &it.Entry.Author, &it.Entry.PublishedAt, &it.Entry.CreatedAt, &it.Feed.ID, &it.Feed.Title); err != nil {
			return Webhook{}, nil, fmt.Errorf("scan digest item: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return Webhook{}, nil, fmt.Errorf("iterate digest items: %w", err)
	}
	return w, items, nil
}

// MarkWebhookLogsSent marks a digest batch delivered: one statement for all
// rows, the webhook counter grows by one message.
func (s *PostgresStore) MarkWebhookLogsSent(ctx context.Context, logIDs []int64, statusCode int, responseSnippet string) error {
	if len(logIDs) == 0 {
		return nil
	}
	const q = `
WITH l AS (
  UPDATE webhook_logs
  SET status = 'sent',
      last_status_code = $2,
      last_error = NULL,
      response_snippet = $3,
      attempt = attempt + 1,
      next_retry_at = NULL,
      updated_at = now()
  WHERE id = ANY($1::bigint[])
  RETURNING webhook_id
)
UPDATE webhooks w
SET sent_total = w.sent_total + 1,
    last_sent_at = now()
WHERE w.id = (SELECT webhook_id FROM l LIMIT 1)`
	if _, err := s.db.Exec(ctx, q, logIDs, statusCode, responseSnippet); err != nil {
		return fmt.Errorf("mark webhook logs sent: %w", err)
	}
	return nil
}

// MarkWebhookLogsFailed is MarkWebhookLogFailed for a digest batch; attempt
// grows per row so a retried batch keeps its history.
func (s *PostgresStore) MarkWebhookLogsFailed(ctx context.Context, logIDs []int64, statusCode *int, errMsg string, responseSnippet string, nextRetryAt *time.Time, dead bool, countAttempt bool) error {
	if len(logIDs) == 0 {
		return nil
	}
	status := "failed"
	if dead {
		status = "dead"
	}
	const q = `
WITH l AS (
  UPDATE webhook_logs
  SET status = $2,
      last_status_code = $3,
      last_error = $4,
      response_snippet = $5,
      attempt = attempt + CASE WHEN $8 THEN 1 ELSE 0 END,
      next_retry_at = $6,
      updated_at = now()
  WHERE id = ANY($1::bigint[])
  RETURNING webhook_id
)
UPDATE webhooks w
SET last_error = $4,
    last_error_at = now(),
    failed_total = w.failed_total + CASE WHEN $7 THEN 1 ELSE 0 END,
    last_failed_at = CASE WHEN $7 THEN now() ELSE w.last_failed_at END
WHERE w.id = (SELECT webhook_id FROM l LIMIT 1)
  AND (w.enabled OR $7)`
	if _, err := s.db.Exec(ctx, q, logIDs, status, statusCode, errMsg, responseSnippet, nextRetryAt, dead, countAttempt); err != nil {
		return fmt.Errorf("mark webhook logs failed: %w", err)
	}
	return nil
}
