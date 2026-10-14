-- user_entries shrinks to exceptions. A row used to exist for every
-- (subscriber, entry) pair although nearly all of them said the same thing:
-- read, not starred. On a 200k-entry instance that was 135 MB (31 % of the
-- database) and 0.98 kB of WAL per new entry and subscriber. From now on a
-- row exists only when the user starred the entry or removed it; visibility
-- follows subscriptions, unread stays in user_unread, read is the absence of
-- both. Lists sort by entries.sort_at, frozen at insert, so the order no
-- longer needs a per-user copy of the timestamp.

ALTER TABLE entries ADD COLUMN IF NOT EXISTS sort_at TIMESTAMPTZ;
UPDATE entries SET sort_at = COALESCE(published_at, created_at) WHERE sort_at IS NULL;
ALTER TABLE entries ALTER COLUMN sort_at SET NOT NULL;
ALTER TABLE entries ALTER COLUMN sort_at SET DEFAULT now();

CREATE INDEX IF NOT EXISTS entries_feed_sort_idx ON entries(feed_id, sort_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS entries_sort_idx ON entries(sort_at DESC, id DESC) INCLUDE (feed_id);
DROP INDEX IF EXISTS entries_feed_created_idx;

ALTER TABLE user_unread DROP CONSTRAINT IF EXISTS user_unread_entry_id_user_id_fkey;
DO $$
BEGIN
  ALTER TABLE user_unread
    ADD CONSTRAINT user_unread_entry_id_fkey FOREIGN KEY (entry_id) REFERENCES entries(id) ON DELETE CASCADE;
  ALTER TABLE user_unread
    ADD CONSTRAINT user_unread_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;

-- Rebuilt instead of DELETEd: 99 % of the rows go away and a fresh heap
-- avoids a table and five indexes full of dead tuples.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema = current_schema() AND table_name = 'user_entries' AND column_name = 'feed_id') THEN
    ALTER TABLE user_entries RENAME TO user_entries_old;
    CREATE TABLE user_entries (
      entry_id   BIGINT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
      user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
      status     TEXT NOT NULL DEFAULT 'read' CHECK (status IN ('read', 'removed')),
      starred    BOOLEAN NOT NULL DEFAULT FALSE,
      sort_at    TIMESTAMPTZ NOT NULL,
      updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
      PRIMARY KEY (entry_id, user_id)
    );
    INSERT INTO user_entries(entry_id, user_id, status, starred, sort_at, updated_at)
    SELECT entry_id, user_id, status, starred, sort_at, updated_at
    FROM user_entries_old
    WHERE starred OR status = 'removed';
    DROP TABLE user_entries_old;
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS user_entries_user_starred_idx ON user_entries(user_id, sort_at DESC, entry_id DESC) WHERE starred;
CREATE INDEX IF NOT EXISTS user_entries_removed_idx ON user_entries(updated_at) WHERE status = 'removed';
CREATE INDEX IF NOT EXISTS user_entries_user_removed_idx ON user_entries(user_id, sort_at DESC, entry_id DESC) WHERE status = 'removed';

-- Explicit storage mode replaces the "hash only" flag. relay keeps a
-- matched entry just long enough for filters and webhooks: no search
-- vector, no per-user rows, no filter_matches.
ALTER TABLE feeds ADD COLUMN IF NOT EXISTS store_mode TEXT NOT NULL DEFAULT 'full';
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_schema = current_schema() AND table_name = 'feeds' AND column_name = 'store_hash_only') THEN
    UPDATE feeds SET store_mode = 'filtered' WHERE store_hash_only;
    ALTER TABLE feeds DROP COLUMN store_hash_only;
  END IF;
END $$;
DO $$
BEGIN
  ALTER TABLE feeds
    ADD CONSTRAINT feeds_store_mode_check CHECK (store_mode IN ('full', 'filtered', 'relay'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;

-- Covered by the unique (webhook_id, entry_id) / (filter_id, entry_id)
-- indexes; the due scan only ever looks at pending and failed rows.
DROP INDEX IF EXISTS webhook_logs_webhook_id_idx;
DROP INDEX IF EXISTS filter_matches_filter_id_idx;
CREATE INDEX IF NOT EXISTS webhook_logs_due_idx ON webhook_logs(next_retry_at) WHERE status IN ('pending', 'failed');
DROP INDEX IF EXISTS webhook_logs_status_next_retry_at_idx;

ANALYZE entries, user_entries, user_unread, feeds;
