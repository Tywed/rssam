-- Full-text search on entries (title + content).
-- Language for indexing is fixed at migration time (simple); runtime FTS_LANGUAGE in the app
-- must match for correct queries (see README).

ALTER TABLE entries
  ADD COLUMN IF NOT EXISTS search_vector tsvector;

UPDATE entries
SET search_vector = to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))
WHERE search_vector IS NULL;

ALTER TABLE entries
  ALTER COLUMN search_vector SET NOT NULL;

CREATE INDEX IF NOT EXISTS entries_search_vector_gin_idx ON entries USING GIN (search_vector);
