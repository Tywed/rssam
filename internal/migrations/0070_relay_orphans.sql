-- Relay entries that never got a delivery row (subscription without a
-- webhook, matched filter without a webhook action, deleted webhook) could
-- only leave the table through REMOVED_RETENTION_DAYS, and were then fetched
-- again as new. From 0.3.3 the poller drops them at once; this moves the
-- ones already there into feed_entry_dedup.
WITH e AS (
  DELETE FROM entries en
  USING feeds f
  WHERE f.id = en.feed_id
    AND f.store_mode = 'relay'
    AND en.removed_at IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM webhook_logs w WHERE w.entry_id = en.id)
  RETURNING en.feed_id, en.hash
)
INSERT INTO feed_entry_dedup(feed_id, hash)
SELECT feed_id, rssam_dedup_key(hash) FROM e WHERE hash <> ''
ON CONFLICT (feed_id, hash) DO NOTHING;
