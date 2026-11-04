-- Everything a poll writes moves out of feeds into a narrow row of its own.
-- A HOT update logs the span of the tuple between the first and the last
-- changed column; in feeds that span covered the URL, title, rules and
-- bridge cursor on every poll. feeds is now written only when a person
-- edits it. No index besides the primary key, so updates stay HOT.
CREATE TABLE IF NOT EXISTS feed_poll_state (
  feed_id BIGINT PRIMARY KEY REFERENCES feeds(id) ON DELETE CASCADE,
  etag TEXT NOT NULL DEFAULT '',
  last_modified TEXT NOT NULL DEFAULT '',
  last_checked_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  next_check_at TIMESTAMPTZ,
  bridge_state JSONB NOT NULL DEFAULT '{}'::jsonb,
  parsing_error_count INT NOT NULL DEFAULT 0,
  poll_paused BOOLEAN NOT NULL DEFAULT FALSE,
  last_entry_at TIMESTAMPTZ,
  items_hash TEXT NOT NULL DEFAULT ''
) WITH (fillfactor = 70);

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'feeds' AND column_name = 'next_check_at') THEN
    INSERT INTO feed_poll_state (feed_id, etag, last_modified, last_checked_at, last_error, next_check_at,
                                 bridge_state, parsing_error_count, poll_paused, last_entry_at, items_hash)
    SELECT id, etag, last_modified, last_checked_at, last_error, next_check_at,
           bridge_state, parsing_error_count, poll_paused, last_entry_at, items_hash
    FROM feeds
    ON CONFLICT (feed_id) DO NOTHING;
    DROP TRIGGER IF EXISTS feeds_due_notify_upd ON feeds;
    ALTER TABLE feeds
      DROP COLUMN etag,
      DROP COLUMN last_modified,
      DROP COLUMN last_checked_at,
      DROP COLUMN last_error,
      DROP COLUMN next_check_at,
      DROP COLUMN bridge_state,
      DROP COLUMN parsing_error_count,
      DROP COLUMN poll_paused,
      DROP COLUMN last_entry_at,
      DROP COLUMN items_hash;
  END IF;
END $$;

INSERT INTO feed_poll_state (feed_id)
SELECT f.id FROM feeds f WHERE NOT EXISTS (SELECT 1 FROM feed_poll_state p WHERE p.feed_id = f.id);

ALTER TABLE feeds RESET (fillfactor);

-- Every feed has exactly one state row from the moment it exists, whoever
-- inserts it (the application, a restore, an operator's SQL): the catalog
-- queries join the two tables without an outer join.
CREATE OR REPLACE FUNCTION rssam_feed_poll_state_init() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO feed_poll_state (feed_id) VALUES (NEW.id) ON CONFLICT (feed_id) DO NOTHING;
  RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS feeds_poll_state_init ON feeds;
CREATE TRIGGER feeds_poll_state_init
  AFTER INSERT ON feeds
  FOR EACH ROW
  EXECUTE FUNCTION rssam_feed_poll_state_init();

-- The due wake-up (0057) follows the columns: a row made due earlier than
-- planned or unpaused by the circuit breaker lives in feed_poll_state now;
-- a manual unpause stays on feeds. feed_poll_state rows are created with
-- the feed, so the insert notification on feeds still covers new feeds.
DROP TRIGGER IF EXISTS feeds_due_notify_upd ON feeds;
CREATE TRIGGER feeds_due_notify_upd
  AFTER UPDATE OF manual_paused ON feeds
  FOR EACH ROW
  WHEN (NEW.manual_paused = FALSE AND OLD.manual_paused)
  EXECUTE FUNCTION rssam_notify_due('rssam_feeds_due');
DROP TRIGGER IF EXISTS feed_poll_state_due_notify_upd ON feed_poll_state;
CREATE TRIGGER feed_poll_state_due_notify_upd
  AFTER UPDATE OF next_check_at, poll_paused ON feed_poll_state
  FOR EACH ROW
  WHEN (NEW.poll_paused = FALSE
        AND (OLD.poll_paused
             OR NEW.next_check_at IS NULL
             OR NEW.next_check_at < OLD.next_check_at))
  EXECUTE FUNCTION rssam_notify_due('rssam_feeds_due');
