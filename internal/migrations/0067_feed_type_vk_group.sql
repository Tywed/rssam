-- 'vk_group' bridge: wall of a VK community or user (https://vk.com/<name>, /public<id>, /club<id>, /id<id>).
ALTER TABLE feeds DROP CONSTRAINT IF EXISTS feeds_feed_type_check;
ALTER TABLE feeds
  ADD CONSTRAINT feeds_feed_type_check CHECK (
    feed_type IN ('rss', 'atom', 'json', 'telegram', 'vk', 'vk_search', 'vk_group', 'max', 'maxstat', 'rutube', 'dzen_news', 'dzen_channel', 'ok_group', 'smotrim', 'page', 'custom', 'inbound')
  );
