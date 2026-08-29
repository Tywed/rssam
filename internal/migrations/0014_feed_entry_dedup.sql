-- Lightweight dedup index for poll cycles (non-matching items and post-webhook retention).

CREATE TABLE IF NOT EXISTS feed_entry_dedup (
  feed_id BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  hash TEXT NOT NULL,
  url TEXT NOT NULL DEFAULT '',
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (feed_id, hash)
);

CREATE INDEX IF NOT EXISTS feed_entry_dedup_feed_id_first_seen_idx ON feed_entry_dedup(feed_id, first_seen_at DESC);
