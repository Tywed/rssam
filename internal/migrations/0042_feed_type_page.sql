-- 'page' bridge: watch a page fragment by CSS selector (page+https://…#selector).
ALTER TABLE feeds DROP CONSTRAINT IF EXISTS feeds_feed_type_check;

ALTER TABLE feeds
  ADD CONSTRAINT feeds_feed_type_check CHECK (
    feed_type IN ('rss', 'atom', 'json', 'telegram', 'vk', 'vk_search', 'max', 'maxstat', 'rutube', 'dzen_news', 'smotrim', 'page', 'custom')
  );
