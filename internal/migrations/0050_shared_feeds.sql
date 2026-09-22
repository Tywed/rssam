-- Feeds become a shared catalog: one row per URL owned by the user who added
-- it (owner_id, NULL once that user is gone). What used to be per-feed
-- user data lives in subscriptions (category, webhook) and per-entry user
-- data in user_entries (status, starred). Same-URL feeds of different users
-- are merged into the row of the lowest owner id: entries with an equal
-- hash collapse into one, the rest move over, every former owner keeps a
-- subscription with their own category/webhook and their read/star state.

CREATE TABLE IF NOT EXISTS subscriptions (
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  feed_id     BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  category_id BIGINT REFERENCES categories(id) ON DELETE SET NULL,
  webhook_id  BIGINT REFERENCES webhooks(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, feed_id)
);
CREATE INDEX IF NOT EXISTS subscriptions_feed_id_idx ON subscriptions(feed_id);
CREATE INDEX IF NOT EXISTS subscriptions_category_id_idx ON subscriptions(category_id);
CREATE INDEX IF NOT EXISTS subscriptions_webhook_id_idx ON subscriptions(webhook_id);

-- feed_id is denormalised for per-feed lists and counters and carries no FK:
-- rows disappear with their entry (entries.feed_id cascades from feeds).
CREATE TABLE IF NOT EXISTS user_entries (
  user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  entry_id   BIGINT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  feed_id    BIGINT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'unread' CHECK (status IN ('unread', 'read', 'removed')),
  starred    BOOLEAN NOT NULL DEFAULT FALSE,
  sort_at    TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (entry_id, user_id)
);

ALTER TABLE feeds ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE entries ADD COLUMN IF NOT EXISTS removed_at TIMESTAMPTZ;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                 WHERE table_schema = current_schema() AND table_name = 'feeds' AND column_name = 'user_id') THEN
    RETURN;
  END IF;

  UPDATE feeds SET owner_id = user_id WHERE owner_id IS NULL;

  CREATE TEMP TABLE rssam_feed_merge ON COMMIT DROP AS
  SELECT f.id AS lose_id, k.keep_id, f.user_id, f.category_id, f.webhook_id
  FROM feeds f
  JOIN (SELECT DISTINCT ON (feed_url) id AS keep_id, feed_url FROM feeds ORDER BY feed_url, user_id, id) k
    ON k.feed_url = f.feed_url AND k.keep_id <> f.id;

  CREATE TEMP TABLE rssam_entry_merge ON COMMIT DROP AS
  SELECT le.id AS lose_entry_id, ke.id AS keep_entry_id
  FROM rssam_feed_merge m
  JOIN entries le ON le.feed_id = m.lose_id
  JOIN entries ke ON ke.feed_id = m.keep_id AND ke.hash = le.hash;

  INSERT INTO user_entries(user_id, entry_id, feed_id, status, starred, sort_at, updated_at)
  SELECT e.user_id, COALESCE(em.keep_entry_id, e.id), COALESCE(fm.keep_id, e.feed_id),
         e.status, e.starred, COALESCE(e.published_at, e.created_at), e.updated_at
  FROM entries e
  LEFT JOIN rssam_entry_merge em ON em.lose_entry_id = e.id
  LEFT JOIN rssam_feed_merge fm ON fm.lose_id = e.feed_id
  ON CONFLICT (entry_id, user_id) DO NOTHING;

  INSERT INTO entry_labels(entry_id, label_id, created_at)
  SELECT em.keep_entry_id, el.label_id, el.created_at
  FROM entry_labels el JOIN rssam_entry_merge em ON em.lose_entry_id = el.entry_id
  ON CONFLICT (entry_id, label_id) DO NOTHING;

  ALTER TABLE enclosures DROP COLUMN user_id;
  INSERT INTO enclosures(entry_id, url, size, mime_type)
  SELECT em.keep_entry_id, en.url, en.size, en.mime_type
  FROM enclosures en JOIN rssam_entry_merge em ON em.lose_entry_id = en.entry_id
  WHERE NOT EXISTS (SELECT 1 FROM enclosures k WHERE k.entry_id = em.keep_entry_id AND k.url = en.url);

  UPDATE webhook_logs wl SET entry_id = em.keep_entry_id
  FROM rssam_entry_merge em
  WHERE wl.entry_id = em.lose_entry_id
    AND NOT EXISTS (SELECT 1 FROM webhook_logs o WHERE o.webhook_id = wl.webhook_id AND o.entry_id = em.keep_entry_id);

  UPDATE filter_matches fm SET entry_id = em.keep_entry_id
  FROM rssam_entry_merge em
  WHERE fm.entry_id = em.lose_entry_id
    AND NOT EXISTS (SELECT 1 FROM filter_matches o WHERE o.filter_id = fm.filter_id AND o.entry_id = em.keep_entry_id);

  DELETE FROM entries e USING rssam_entry_merge em WHERE e.id = em.lose_entry_id;

  UPDATE entries e SET feed_id = m.keep_id FROM rssam_feed_merge m WHERE e.feed_id = m.lose_id;

  INSERT INTO feed_entry_dedup(feed_id, hash, url, first_seen_at)
  SELECT m.keep_id, d.hash, d.url, d.first_seen_at
  FROM feed_entry_dedup d JOIN rssam_feed_merge m ON m.lose_id = d.feed_id
  ON CONFLICT (feed_id, hash) DO UPDATE SET first_seen_at = LEAST(feed_entry_dedup.first_seen_at, EXCLUDED.first_seen_at);

  UPDATE feed_poll_log l SET feed_id = m.keep_id FROM rssam_feed_merge m WHERE l.feed_id = m.lose_id;
  UPDATE filter_scope_items i SET feed_id = m.keep_id FROM rssam_feed_merge m WHERE i.feed_id = m.lose_id;

  INSERT INTO subscriptions(user_id, feed_id, category_id, webhook_id)
  SELECT user_id, keep_id, category_id, webhook_id FROM rssam_feed_merge
  ON CONFLICT (user_id, feed_id) DO NOTHING;

  DELETE FROM feeds f USING rssam_feed_merge m WHERE f.id = m.lose_id;

  INSERT INTO subscriptions(user_id, feed_id, category_id, webhook_id)
  SELECT user_id, id, category_id, webhook_id FROM feeds
  ON CONFLICT (user_id, feed_id) DO NOTHING;

  UPDATE feeds f SET last_entry_at = GREATEST(f.last_entry_at, x.last_entry)
  FROM (SELECT feed_id, max(created_at) AS last_entry FROM entries GROUP BY feed_id) x
  WHERE x.feed_id = f.id AND f.id IN (SELECT keep_id FROM rssam_feed_merge);

  UPDATE entries SET removed_at = updated_at
  WHERE status = 'removed' AND title = '' AND content = '';

  ALTER TABLE entries DROP COLUMN user_id, DROP COLUMN status, DROP COLUMN starred;
  ALTER TABLE feeds DROP COLUMN user_id, DROP COLUMN category_id, DROP COLUMN webhook_id;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS feeds_feed_url_uidx ON feeds(feed_url);

-- Unread/read/removed lists, "all" list, starred list, per-feed lists and
-- counters, removed-row purge. Status is in every index, so a status change
-- is never a HOT update; the row is ~60 bytes against ~1.7 kB before.
CREATE INDEX IF NOT EXISTS user_entries_user_status_sort_idx ON user_entries(user_id, status, sort_at DESC, entry_id DESC);
CREATE INDEX IF NOT EXISTS user_entries_user_sort_idx ON user_entries(user_id, sort_at DESC, entry_id DESC) WHERE status <> 'removed';
CREATE INDEX IF NOT EXISTS user_entries_user_starred_idx ON user_entries(user_id, sort_at DESC, entry_id DESC) WHERE starred;
CREATE INDEX IF NOT EXISTS user_entries_user_feed_status_idx ON user_entries(user_id, feed_id, status);
CREATE INDEX IF NOT EXISTS user_entries_removed_idx ON user_entries(updated_at) WHERE status = 'removed';

CREATE INDEX IF NOT EXISTS entries_feed_created_idx ON entries(feed_id, created_at DESC);
CREATE INDEX IF NOT EXISTS entries_removed_at_idx ON entries(removed_at) WHERE removed_at IS NOT NULL;

ANALYZE feeds, entries, user_entries, subscriptions;
