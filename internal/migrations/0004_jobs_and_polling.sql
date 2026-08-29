-- Jobs queue + polling scheduling.

ALTER TABLE feeds
  ADD COLUMN IF NOT EXISTS next_check_at TIMESTAMPTZ;

-- Backfill for existing feeds: start polling immediately.
UPDATE feeds
SET next_check_at = now()
WHERE next_check_at IS NULL;

CREATE TABLE IF NOT EXISTS jobs (
  id BIGSERIAL PRIMARY KEY,
  type TEXT NOT NULL,
  feed_id BIGINT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  run_at TIMESTAMPTZ NOT NULL,
  payload JSONB NULL,
  attempts INT NOT NULL DEFAULT 0,
  last_error TEXT NULL,
  locked_at TIMESTAMPTZ NULL,
  locked_by TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS jobs_run_at_idx ON jobs(run_at);
CREATE INDEX IF NOT EXISTS jobs_type_run_at_idx ON jobs(type, run_at);

-- Prevent duplicates for poll_feed per feed_id.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_poll_feed_uidx
  ON jobs(type, feed_id)
  WHERE type = 'poll_feed' AND feed_id IS NOT NULL;

