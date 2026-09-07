-- Persistent per-webhook delivery counters.
--
-- Until now "Отправлено" / "Мёртвые" on the webhooks dashboard were computed
-- from webhook_logs, so they silently dropped to zero as soon as the retention
-- cleanup (WEBHOOK_LOG_RETENTION_DAYS) purged old rows. The counters below are
-- maintained by MarkWebhookLogSent / MarkWebhookLogFailed and survive log
-- retention; only an explicit "reset" by the user clears them.
--
--   sent_total     successful deliveries
--   failed_total   deliveries that gave up (log status = 'dead')
--   last_sent_at   time of the last successful delivery
--   last_failed_at time of the last final failure (dead)
--   last_error     text of the last failed attempt (any, incl. retried ones)
--   last_error_at  time of that attempt
--   stats_reset_at when the counters were last reset by the user (NULL = never)

ALTER TABLE webhooks
  ADD COLUMN IF NOT EXISTS sent_total BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS failed_total BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_sent_at TIMESTAMPTZ NULL,
  ADD COLUMN IF NOT EXISTS last_failed_at TIMESTAMPTZ NULL,
  ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_error_at TIMESTAMPTZ NULL,
  ADD COLUMN IF NOT EXISTS stats_reset_at TIMESTAMPTZ NULL;

-- Backfill from whatever logs are still present, so existing installations do
-- not start from zero.
UPDATE webhooks w
SET sent_total = s.sent_total,
    failed_total = s.failed_total,
    last_sent_at = s.last_sent_at,
    last_failed_at = s.last_failed_at,
    last_error = COALESCE(s.last_error, ''),
    last_error_at = s.last_error_at
FROM (
  SELECT
    wl.webhook_id,
    COUNT(*) FILTER (WHERE wl.status = 'sent') AS sent_total,
    COUNT(*) FILTER (WHERE wl.status = 'dead') AS failed_total,
    MAX(wl.updated_at) FILTER (WHERE wl.status = 'sent') AS last_sent_at,
    MAX(wl.updated_at) FILTER (WHERE wl.status = 'dead') AS last_failed_at,
    (
      SELECT wl2.last_error
      FROM webhook_logs wl2
      WHERE wl2.webhook_id = wl.webhook_id AND COALESCE(wl2.last_error, '') <> ''
      ORDER BY wl2.updated_at DESC, wl2.id DESC
      LIMIT 1
    ) AS last_error,
    MAX(wl.updated_at) FILTER (WHERE COALESCE(wl.last_error, '') <> '') AS last_error_at
  FROM webhook_logs wl
  GROUP BY wl.webhook_id
) s
WHERE s.webhook_id = w.id
  AND w.sent_total = 0
  AND w.failed_total = 0;
