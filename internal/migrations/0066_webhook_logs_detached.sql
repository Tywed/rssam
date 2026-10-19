-- A relay entry used to be emptied after delivery (title/content → '',
-- removed_at) and deleted 30 days later by retention, because webhook_logs
-- referenced it with ON DELETE CASCADE. The log row now records the feed
-- itself and keeps entry_id as a plain number, so the entry can be deleted
-- the moment its last delivery is done: strip+dedup 810 B of WAL → delete+dedup
-- ~200 B, no second write at retention time, no stripped rows waiting.
-- Old rows keep working: the log page falls back to the entry's feed while
-- the entry exists. Pending deliveries of an entry deleted by a user end as
-- "delivery context not found" instead of vanishing with it.
ALTER TABLE webhook_logs DROP CONSTRAINT IF EXISTS webhook_logs_entry_id_fkey;
ALTER TABLE webhook_logs ADD COLUMN IF NOT EXISTS feed_id BIGINT;
