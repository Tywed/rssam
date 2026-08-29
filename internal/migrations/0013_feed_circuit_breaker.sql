-- Circuit breaker: consecutive poll failures pause background polling until manual refresh.
ALTER TABLE feeds
  ADD COLUMN IF NOT EXISTS parsing_error_count INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS poll_paused BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_feeds_poll_paused_next_check
  ON feeds (poll_paused, next_check_at)
  WHERE poll_paused = FALSE;
