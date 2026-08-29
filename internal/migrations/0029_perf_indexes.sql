-- Partial indexes for unread counts, job claim, and due-feed scheduling.

CREATE INDEX IF NOT EXISTS entries_user_unread_feed_idx
  ON entries (user_id, feed_id)
  WHERE status = 'unread';

CREATE INDEX IF NOT EXISTS jobs_due_unlocked_idx
  ON jobs (run_at, id)
  WHERE locked_at IS NULL;

CREATE INDEX IF NOT EXISTS feeds_due_poll_idx
  ON feeds (next_check_at, id)
  WHERE poll_paused = FALSE AND manual_paused = FALSE;

DROP INDEX IF EXISTS users_fever_api_key_idx;
