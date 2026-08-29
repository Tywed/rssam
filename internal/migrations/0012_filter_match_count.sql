-- Denormalized match counter for filters (incremented on new filter_matches rows).

ALTER TABLE filters
  ADD COLUMN IF NOT EXISTS match_count BIGINT NOT NULL DEFAULT 0;
