-- When did the feed last deliver something new. Written by the poll UPDATE
-- that already touches the row (no extra row version), so the admin list can
-- show feeds that answer 200 but have been silent for weeks. Backfilled from
-- the newest entry / dedup hash so the column is meaningful right away.

ALTER TABLE feeds ADD COLUMN IF NOT EXISTS last_entry_at TIMESTAMPTZ NULL;

UPDATE feeds f
SET last_entry_at = GREATEST(
  (SELECT max(e.created_at) FROM entries e WHERE e.feed_id = f.id),
  (SELECT max(d.first_seen_at) FROM feed_entry_dedup d WHERE d.feed_id = f.id)
)
WHERE f.last_entry_at IS NULL;
