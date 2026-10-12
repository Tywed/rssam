-- "Unread" becomes the presence of a row in user_unread instead of a value
-- of user_entries.status. Marking read used to be a non-HOT UPDATE that
-- touched every index of user_entries: 7.6 kB of WAL per entry (66 % of it
-- full-page images of user_entries_user_feed_sort_idx). A DELETE from this
-- narrow table costs 0.4 kB (-95 %), the unread list of a feed reads 6
-- buffers instead of 117 and the dead tuples that reading leaves behind
-- live in a 5 MB table instead of the 100 MB one. user_entries keeps
-- starred and the read/removed distinction, which rarely change.
--
-- The composite FK replaces separate ones to entries and users: one check
-- per insert, and a user_entries row takes its unread marker with it.
CREATE TABLE IF NOT EXISTS user_unread (
  entry_id BIGINT NOT NULL,
  user_id  BIGINT NOT NULL,
  feed_id  BIGINT NOT NULL,
  sort_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (entry_id, user_id),
  FOREIGN KEY (entry_id, user_id) REFERENCES user_entries(entry_id, user_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS user_unread_user_feed_idx ON user_unread(user_id, feed_id, sort_at DESC, entry_id DESC);
CREATE INDEX IF NOT EXISTS user_unread_user_sort_idx ON user_unread(user_id, sort_at DESC, entry_id DESC);

INSERT INTO user_unread(entry_id, user_id, feed_id, sort_at)
SELECT entry_id, user_id, feed_id, sort_at FROM user_entries WHERE status = 'unread'
ON CONFLICT DO NOTHING;

-- Dropped before the rewrite below so that it maintains two indexes fewer.
DROP INDEX IF EXISTS user_entries_user_status_sort_idx;
DROP INDEX IF EXISTS user_entries_user_feed_unread_idx;

UPDATE user_entries SET status = 'read' WHERE status = 'unread';

ALTER TABLE user_entries ALTER COLUMN status SET DEFAULT 'read';
ALTER TABLE user_entries DROP CONSTRAINT IF EXISTS user_entries_status_check;
DO $$
BEGIN
  ALTER TABLE user_entries
    ADD CONSTRAINT user_entries_status_check CHECK (status IN ('read', 'removed'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;

ANALYZE user_entries, user_unread;
