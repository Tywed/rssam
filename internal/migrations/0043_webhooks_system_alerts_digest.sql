-- system_alerts: the webhook also receives instance notifications (feed
-- auto-paused, feeds gone silent, a webhook delivery gave up, update
-- available, backup stale). digest_minutes > 0 batches entry deliveries into
-- one message per window instead of one per entry.
ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS system_alerts BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS digest_minutes INT NOT NULL DEFAULT 0;

ALTER TABLE webhooks DROP CONSTRAINT IF EXISTS webhooks_digest_minutes_check;
ALTER TABLE webhooks
  ADD CONSTRAINT webhooks_digest_minutes_check CHECK (digest_minutes >= 0 AND digest_minutes <= 1440);

-- The delivery claim reads webhook_logs_status_next_retry_at_idx as a range
-- (next_retry_at <= now()); pending/failed rows without a time would never be
-- claimed. Application code has always set it; this guards hand-edited rows.
UPDATE webhook_logs SET next_retry_at = created_at
WHERE status IN ('pending', 'failed') AND next_retry_at IS NULL;
