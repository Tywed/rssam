-- Poll log is an event journal (state changes), not one row per attempt.
-- The 0.1.8 table wrote every poll; with ~2k feeds that was ~1M rows/day.
-- Drop the accumulated rows: they are not useful as a per-attempt trace, and
-- the new writer keeps at most MaxFeedPollLogPerFeed (30) rows per feed.
--
-- repeat_count: consecutive identical failures are coalesced into one row.

TRUNCATE feed_poll_log;

ALTER TABLE feed_poll_log
  ADD COLUMN IF NOT EXISTS repeat_count INTEGER NOT NULL DEFAULT 1;
