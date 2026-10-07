-- Categories become shared objects of the editors (0.2.2): a feed belongs
-- to exactly one category for everybody (feeds.category_id), readers
-- follow categories as a whole (category_followers) or subscribe to single
-- feeds. Personal categories and collections go away.
--
-- Data: a feed takes the category its owner filed it under, else the
-- earliest subscriber's one. Categories with the same title (case- and
-- space-insensitive) merge into the oldest row. A user who was subscribed
-- to every feed of a category becomes its follower; empty categories of
-- readers are dropped.

ALTER TABLE feeds ADD COLUMN IF NOT EXISTS category_id BIGINT REFERENCES categories(id) ON DELETE RESTRICT;

CREATE TABLE IF NOT EXISTS category_followers (
  category_id BIGINT NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (category_id, user_id)
);
CREATE INDEX IF NOT EXISTS category_followers_user_id_idx ON category_followers(user_id);

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'subscriptions' AND column_name = 'category_id') THEN
    UPDATE feeds f SET category_id = COALESCE(
      (SELECT s.category_id FROM subscriptions s WHERE s.feed_id = f.id AND s.user_id = f.owner_id),
      (SELECT s.category_id FROM subscriptions s WHERE s.feed_id = f.id AND s.category_id IS NOT NULL ORDER BY s.created_at, s.user_id LIMIT 1))
    WHERE f.category_id IS NULL;

    -- Merge same-titled categories into the oldest one.
    CREATE TEMP TABLE rssam_cat_merge ON COMMIT DROP AS
      SELECT c.id AS lose_id, k.keep_id
      FROM categories c
      JOIN (SELECT lower(btrim(title)) AS key, min(id) AS keep_id FROM categories GROUP BY 1) k
        ON k.key = lower(btrim(c.title))
      WHERE c.id <> k.keep_id;
    UPDATE feeds f SET category_id = m.keep_id FROM rssam_cat_merge m WHERE f.category_id = m.lose_id;
    UPDATE filter_scope_items i SET category_id = m.keep_id FROM rssam_cat_merge m
      WHERE i.category_id = m.lose_id
        AND NOT EXISTS (SELECT 1 FROM filter_scope_items j WHERE j.filter_id = i.filter_id AND j.category_id = m.keep_id);
    DELETE FROM filter_scope_items i USING rssam_cat_merge m WHERE i.category_id = m.lose_id;
    DELETE FROM categories c USING rssam_cat_merge m WHERE c.id = m.lose_id;

    -- Subscribed to every feed of a category → follows it.
    INSERT INTO category_followers(category_id, user_id)
    SELECT f.category_id, s.user_id
    FROM feeds f JOIN subscriptions s ON s.feed_id = f.id
    WHERE f.category_id IS NOT NULL
    GROUP BY f.category_id, s.user_id
    HAVING count(*) = (SELECT count(*) FROM feeds g WHERE g.category_id = f.category_id)
    ON CONFLICT DO NOTHING;

    -- Readers keep no categories of their own; their leftovers that hold
    -- no feed would only clutter the shared list.
    DELETE FROM categories c USING users u
      WHERE u.id = c.user_id AND u.role = 'reader'
        AND NOT EXISTS (SELECT 1 FROM feeds f WHERE f.category_id = c.id);

    UPDATE categories c SET title = btrim(title) WHERE title <> btrim(title);
    ALTER TABLE subscriptions DROP COLUMN category_id, DROP COLUMN IF EXISTS collection_id;
    ALTER TABLE categories DROP COLUMN user_id;
  END IF;
END $$;

DROP TABLE IF EXISTS collection_followers;
DROP TABLE IF EXISTS collection_feeds;
DROP TABLE IF EXISTS collections;

CREATE UNIQUE INDEX IF NOT EXISTS categories_title_key ON categories (lower(btrim(title)));
CREATE INDEX IF NOT EXISTS feeds_category_id_idx ON feeds(category_id);
CREATE INDEX IF NOT EXISTS categories_sort_order_idx ON categories(sort_order, id);
