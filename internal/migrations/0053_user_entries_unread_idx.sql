-- Unread counters (sidebar per feed/category, totals) scan only unread
-- rows: a partial index over them is a fraction of the full ones (416 KB vs
-- 32-38 MB at 200k rows / 60k unread) and reads nothing else. Index-only
-- for every counter query; rows leave it as they are marked read.
CREATE INDEX IF NOT EXISTS user_entries_user_feed_unread_idx ON user_entries(user_id, feed_id) WHERE status = 'unread';
