-- starred entries, feed icons, RSS enclosures.

ALTER TABLE entries
  ADD COLUMN IF NOT EXISTS starred BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS entries_user_id_starred_idx ON entries(user_id, starred) WHERE starred = TRUE;

ALTER TABLE feeds
  ADD COLUMN IF NOT EXISTS icon_url TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS icon_data BYTEA;

CREATE TABLE IF NOT EXISTS enclosures (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  entry_id BIGINT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  url TEXT NOT NULL,
  size BIGINT NOT NULL DEFAULT 0,
  mime_type TEXT NOT NULL DEFAULT '',
  media_progression INT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS enclosures_entry_id_idx ON enclosures(entry_id);
CREATE INDEX IF NOT EXISTS enclosures_user_id_idx ON enclosures(user_id);
