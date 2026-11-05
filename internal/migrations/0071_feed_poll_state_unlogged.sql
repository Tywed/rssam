-- feed_poll_state carries only what a poll can rebuild (HTTP validators,
-- bridge cursors, error counters, schedule). As an UNLOGGED table its
-- ~500 000 updates a day no longer go through the WAL: 251 B → 58 B per
-- poll on the bench. A PostgreSQL crash (not a restart) truncates it; the
-- application re-creates the rows on start and after the due listener
-- reconnects, and every feed is then fetched once in full — duplicates are
-- held back by feed_entry_dedup, which stays logged.
ALTER TABLE feed_poll_state SET UNLOGGED;
