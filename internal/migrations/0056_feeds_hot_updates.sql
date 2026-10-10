-- feeds: every poll rewrites the row (next_check_at, last_checked_at, ETag…).
-- With next_check_at indexed that update was never HOT: a new tuple plus an
-- entry in all four indexes, 568 B of WAL per poll. Without the index and
-- with 20 % slack per page it is a HOT update inside the page: 211 B, no
-- index writes. The scheduler reads feeds sequentially instead (1 000 feeds
-- = 34 buffers, 0.4 ms). Pages filled before this migration gain their slack
-- as the old tuple versions are pruned after the first update.
DROP INDEX IF EXISTS feeds_due_poll_idx;
ALTER TABLE feeds SET (fillfactor = 80);
