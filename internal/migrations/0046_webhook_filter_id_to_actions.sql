-- Legacy webhook↔filter binding (webhooks.filter_id, 0006) becomes a regular
-- filter action; the refresher no longer has a second routing path. The
-- column stays (unused, always NULL) until 0048.
INSERT INTO filter_actions (filter_id, action_type, action_param, priority)
SELECT w.filter_id, 'webhook', w.id::text, 0
FROM webhooks w
WHERE w.filter_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM filter_actions fa
    WHERE fa.filter_id = w.filter_id
      AND fa.action_type = 'webhook'
      AND fa.action_param = w.id::text
  );

UPDATE webhooks SET filter_id = NULL WHERE filter_id IS NOT NULL;

DROP INDEX IF EXISTS webhooks_filter_id_idx;
