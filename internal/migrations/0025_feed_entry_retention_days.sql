-- Per-feed retention: delete entries and dedup hashes older than N days.

ALTER TABLE feeds
  ADD COLUMN IF NOT EXISTS entry_retention_days INTEGER;

DO $$
BEGIN
  ALTER TABLE feeds
    ADD CONSTRAINT feeds_entry_retention_days_check
    CHECK (entry_retention_days IS NULL OR entry_retention_days BETWEEN 1 AND 3650);
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;
