-- Webhooks + delivery logs.

CREATE TABLE IF NOT EXISTS webhooks (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL DEFAULT 0,
  filter_id BIGINT NULL REFERENCES filters(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  method TEXT NOT NULL DEFAULT 'POST',
  headers JSONB NOT NULL DEFAULT '{}'::jsonb,
  body_template TEXT NOT NULL DEFAULT '',
  secret TEXT NOT NULL DEFAULT '',
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS webhooks_user_id_idx ON webhooks(user_id);
CREATE INDEX IF NOT EXISTS webhooks_filter_id_idx ON webhooks(filter_id);
CREATE INDEX IF NOT EXISTS webhooks_enabled_idx ON webhooks(enabled);

CREATE TABLE IF NOT EXISTS webhook_logs (
  id BIGSERIAL PRIMARY KEY,
  webhook_id BIGINT NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  entry_id BIGINT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending',
  attempt INT NOT NULL DEFAULT 0,
  next_retry_at TIMESTAMPTZ NULL,
  last_status_code INT NULL,
  last_error TEXT NULL,
  response_snippet TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
  ALTER TABLE webhook_logs
    ADD CONSTRAINT webhook_logs_status_check CHECK (status IN ('pending', 'sent', 'failed', 'dead'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;

-- Idempotency for enqueue: at most one delivery per (webhook, entry).
CREATE UNIQUE INDEX IF NOT EXISTS webhook_logs_webhook_id_entry_id_uidx ON webhook_logs(webhook_id, entry_id);

CREATE INDEX IF NOT EXISTS webhook_logs_status_next_retry_at_idx ON webhook_logs(status, next_retry_at);
CREATE INDEX IF NOT EXISTS webhook_logs_webhook_id_idx ON webhook_logs(webhook_id);
CREATE INDEX IF NOT EXISTS webhook_logs_entry_id_idx ON webhook_logs(entry_id);

