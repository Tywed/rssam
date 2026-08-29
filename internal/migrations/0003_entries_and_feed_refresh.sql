-- entries: расширяем схему под reader MVP.
-- В базе уже могла существовать "MVP" таблица entries из 0001_init.sql.

ALTER TABLE feeds
  ADD COLUMN IF NOT EXISTS etag TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_modified TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS last_checked_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';

ALTER TABLE entries
  ADD COLUMN IF NOT EXISTS content TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS author TEXT,
  ADD COLUMN IF NOT EXISTS hash TEXT,
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'unread',
  ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Backfill hash для существующих строк без расширений (используем встроенный md5).
UPDATE entries
SET hash = md5(coalesce(url, ''))
WHERE hash IS NULL OR hash = '';

ALTER TABLE entries
  ALTER COLUMN hash SET NOT NULL;

DO $$
BEGIN
  ALTER TABLE entries
    ADD CONSTRAINT entries_status_check CHECK (status IN ('unread', 'read', 'removed'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;

-- Уникальность (feed_id, hash): создаём индекс с защитой от повторного создания.
CREATE UNIQUE INDEX IF NOT EXISTS entries_feed_id_hash_uidx ON entries(feed_id, hash);

-- Для списков по фиду: feed_id + status + id desc.
CREATE INDEX IF NOT EXISTS entries_feed_id_status_id_desc_idx ON entries(feed_id, status, id DESC);

