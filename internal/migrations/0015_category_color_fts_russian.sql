-- Category color marker for feed tree UI.
ALTER TABLE categories ADD COLUMN IF NOT EXISTS color VARCHAR(7) NOT NULL DEFAULT '';

-- Reindex FTS with russian config (simple ignores Cyrillic).
UPDATE entries
SET search_vector = to_tsvector('russian',
  coalesce(title, '') || ' ' || coalesce(regexp_replace(content, '<[^>]+>', ' ', 'g'), ''))
WHERE search_vector IS NOT NULL;
