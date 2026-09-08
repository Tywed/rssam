-- Per-feed poll history.
--
-- feeds.last_error / parsing_error_count only describe the *current* state of
-- a feed. When a feed flaps (one failure, then recovery) the failure leaves no
-- trace, which makes "why was this feed paused yesterday?" unanswerable. This
-- table keeps one row per poll attempt (success or failure) and is trimmed by
-- the retention cleanup (FEED_POLL_LOG_RETENTION_DAYS).

CREATE TABLE IF NOT EXISTS feed_poll_log (
  id          BIGSERIAL PRIMARY KEY,
  feed_id     BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  ok          BOOLEAN NOT NULL,
  error       TEXT NOT NULL DEFAULT '',
  inserted    INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0
);

-- "last N polls of feed X" on the admin feed page.
CREATE INDEX IF NOT EXISTS feed_poll_log_feed_at_idx ON feed_poll_log (feed_id, at DESC);
-- retention cleanup by age.
CREATE INDEX IF NOT EXISTS feed_poll_log_at_idx ON feed_poll_log (at);
