-- feed_poll_log_at_idx served only the retention DELETE (WHERE at < $1).
-- The table is capped per feed (≈27 rows × feeds), so that scan is a few
-- hundred pages once per cleanup, while the index was maintained on every
-- INSERT/DELETE and had grown to 10× its live size (14 MB for 60 000 rows).
-- The two remaining indexes are rebuilt for the same reason.
DROP INDEX IF EXISTS feed_poll_log_at_idx;
REINDEX INDEX feed_poll_log_pkey;
REINDEX INDEX feed_poll_log_feed_at_idx;
