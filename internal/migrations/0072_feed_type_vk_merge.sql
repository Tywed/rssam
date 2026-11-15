-- One 'vk' bridge: searches (vk-search://q) and walls (https://vk.com/name)
-- are told apart by the feed URL. 'vk_search' / 'vk_group' rows become 'vk';
-- the search cursor moves to the bridge_state key the merged bridge reads.
UPDATE feeds SET feed_type = 'vk' WHERE feed_type IN ('vk_search', 'vk_group');
UPDATE feed_poll_state
   SET bridge_state = (bridge_state - 'vk_search') || jsonb_build_object('vk', bridge_state->'vk_search')
 WHERE bridge_state ? 'vk_search';
ALTER TABLE feeds DROP CONSTRAINT IF EXISTS feeds_feed_type_check;
ALTER TABLE feeds
  ADD CONSTRAINT feeds_feed_type_check CHECK (
    feed_type IN ('rss', 'atom', 'json', 'telegram', 'vk', 'max', 'maxstat', 'rutube', 'dzen_news', 'dzen_channel', 'ok_group', 'smotrim', 'page', 'custom', 'inbound')
  );
