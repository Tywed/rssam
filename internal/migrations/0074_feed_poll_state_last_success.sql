-- When the feed was last polled without an error. last_checked_at moves on
-- failures too, so "checked an hour ago" says nothing about whether the
-- source still answers; the admin table and alerts need the second stamp.
-- Backfill: a feed without a current error was fine at its last check.
ALTER TABLE feed_poll_state ADD COLUMN IF NOT EXISTS last_success_at TIMESTAMPTZ NULL;
UPDATE feed_poll_state
SET last_success_at = last_checked_at
WHERE last_success_at IS NULL
  AND last_checked_at IS NOT NULL
  AND COALESCE(last_error, '') = ''
  AND parsing_error_count = 0;
