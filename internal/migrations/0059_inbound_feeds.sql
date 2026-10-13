-- An inbound feed (feed_type = 'inbound') is never polled: its entries are
-- POSTed to /hooks/{id} by another rssam, signed with this secret.
ALTER TABLE feeds ADD COLUMN IF NOT EXISTS inbound_secret TEXT NOT NULL DEFAULT '';

ALTER TABLE feeds DROP CONSTRAINT IF EXISTS feeds_feed_type_check;
ALTER TABLE feeds
  ADD CONSTRAINT feeds_feed_type_check CHECK (
    feed_type IN ('rss', 'atom', 'json', 'telegram', 'vk', 'vk_search', 'max', 'maxstat', 'rutube', 'dzen_news', 'dzen_channel', 'smotrim', 'page', 'custom', 'inbound')
  );
