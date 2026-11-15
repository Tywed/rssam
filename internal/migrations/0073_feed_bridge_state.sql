-- Bridge cursors (Telegram / MAX / VK offsets, page hashes) move out of the
-- UNLOGGED feed_poll_state into a logged table: after a PostgreSQL crash a
-- bridge continues from its cursor instead of re-reading the source from
-- the start. A cursor changes only when the source does, so the table sees
-- a few thousand small writes a day, not one per poll.
CREATE TABLE IF NOT EXISTS feed_bridge_state (
  feed_id BIGINT PRIMARY KEY REFERENCES feeds(id) ON DELETE CASCADE,
  state   JSONB NOT NULL
);
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name = 'feed_poll_state' AND column_name = 'bridge_state') THEN
    INSERT INTO feed_bridge_state (feed_id, state)
    SELECT feed_id, bridge_state FROM feed_poll_state
    WHERE bridge_state IS NOT NULL AND bridge_state <> '{}'::jsonb
    ON CONFLICT (feed_id) DO NOTHING;
    ALTER TABLE feed_poll_state DROP COLUMN bridge_state;
  END IF;
END $$;
