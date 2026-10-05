-- Collections: a named set of catalog feeds curated by an editor or admin
-- that a reader follows as a whole. Following subscribes to every feed of
-- the set into one category of the reader and keeps the membership in
-- sync: feeds added to the set later are subscribed, feeds removed from it
-- are unsubscribed. subscriptions.collection_id marks the rows a follow
-- created, so unfollowing removes exactly those and leaves subscriptions
-- the reader made by hand. A feed that belongs to a collection stays in
-- the catalog even with no subscribers.

CREATE TABLE IF NOT EXISTS collections (
  id          BIGSERIAL PRIMARY KEY,
  owner_id    BIGINT REFERENCES users(id) ON DELETE SET NULL,
  title       TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  description TEXT NOT NULL DEFAULT '' CHECK (length(description) <= 2000),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS collection_feeds (
  collection_id BIGINT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
  feed_id       BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  added_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (collection_id, feed_id)
);
CREATE INDEX IF NOT EXISTS collection_feeds_feed_id_idx ON collection_feeds(feed_id);

CREATE TABLE IF NOT EXISTS collection_followers (
  collection_id BIGINT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
  user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  category_id   BIGINT REFERENCES categories(id) ON DELETE SET NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (collection_id, user_id)
);
CREATE INDEX IF NOT EXISTS collection_followers_user_id_idx ON collection_followers(user_id);
CREATE INDEX IF NOT EXISTS collection_followers_category_id_idx ON collection_followers(category_id);

ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS collection_id BIGINT REFERENCES collections(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS subscriptions_collection_id_idx ON subscriptions(collection_id) WHERE collection_id IS NOT NULL;
