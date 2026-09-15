-- jobs is a transient queue: a poll_feed row lives for seconds between
-- enqueue, claim and delete, and the scheduler re-derives every due job from
-- feeds.next_check_at on its next tick. WAL-logging it protected nothing and
-- cost 927 B per poll (INSERT + claim UPDATE + DELETE, PG 17); UNLOGGED brings
-- the cycle to 201 B. After a crash the table comes back empty, which is the
-- same state a clean start begins from.
ALTER TABLE jobs SET UNLOGGED;

-- entries: content and search_vector (~1.5 kB each on typical feeds) sat
-- inline next to status/starred, so every mark-read rewrote a 3 kB tuple and
-- re-inserted the tsvector into the GIN index (the update is never HOT: status
-- is indexed). Storing large values out of line from the start cuts a
-- mark-read from 4 047 B to 2 630 B of WAL and the heap from 47 MB to 4 MB per
-- 20 000 rows; the list page reads the same 7 buffers, an entry read adds one
-- TOAST lookup. Applies to rows written from now on; existing rows move as
-- they are next updated.
ALTER TABLE entries SET (toast_tuple_target = 128);
