-- Indexes superseded by 0029 (jobs_due_unlocked_idx, feeds_due_poll_idx) and
-- 0009 (feeds_user_id_feed_url_uidx). Every write to jobs (enqueue, claim,
-- reschedule, delete — several per poll) and to feeds maintained them for
-- nothing: no query uses (type, run_at) or plain run_at, the partial
-- (poll_paused, next_check_at) index is a subset of feeds_due_poll_idx, and
-- feeds_user_id_idx is a prefix of the unique (user_id, feed_url) index.
-- Measured on 2 000 feeds: one full poll round of jobs traffic writes ~18%
-- less WAL, the feeds row-update round ~10% less.

DROP INDEX IF EXISTS jobs_run_at_idx;
DROP INDEX IF EXISTS jobs_type_run_at_idx;
DROP INDEX IF EXISTS idx_feeds_poll_paused_next_check;
DROP INDEX IF EXISTS feeds_user_id_idx;
