ALTER TABLE feeds
  ADD COLUMN IF NOT EXISTS feed_type VARCHAR(20) NOT NULL DEFAULT 'rss',
  ADD COLUMN IF NOT EXISTS bridge_state JSONB NOT NULL DEFAULT '{}';

DO $$
BEGIN
  ALTER TABLE feeds
    ADD CONSTRAINT feeds_feed_type_check CHECK (feed_type IN ('rss', 'atom', 'json', 'telegram', 'vk', 'max', 'custom'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;
