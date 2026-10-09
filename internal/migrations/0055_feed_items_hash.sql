-- items_hash fingerprints the set of item hashes the source delivered on
-- the last poll. When the next poll yields the same set, nothing can be
-- inserted, so the core skips CreateEntries (tsvector for every item,
-- dedup and unique-index probes) altogether.
ALTER TABLE feeds ADD COLUMN IF NOT EXISTS items_hash text NOT NULL DEFAULT '';
