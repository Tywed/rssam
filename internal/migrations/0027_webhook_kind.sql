-- Webhook destination kinds: generic HTTP, Telegram Bot API, PyMax HTTP API.

ALTER TABLE webhooks
  ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'http';

ALTER TABLE webhooks
  ADD COLUMN IF NOT EXISTS provider_config JSONB NOT NULL DEFAULT '{}'::jsonb;

DO $$
BEGIN
  ALTER TABLE webhooks
    ADD CONSTRAINT webhooks_kind_check CHECK (kind IN ('http', 'telegram', 'max'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;
