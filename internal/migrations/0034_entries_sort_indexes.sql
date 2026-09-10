-- Entry lists order by COALESCE(published_at, created_at) DESC, id DESC
-- (storage/entry_sort.go); until now no index covered that expression, so every
-- page sorted the whole user's set. entries_user_id_idx and entries_feed_id_idx
-- are prefixes of the indexes below / of entries_feed_id_status_id_desc_idx.

CREATE INDEX IF NOT EXISTS entries_user_status_sort_idx
  ON entries (user_id, status, (COALESCE(published_at, created_at)) DESC NULLS LAST, id DESC);

CREATE INDEX IF NOT EXISTS entries_user_sort_idx
  ON entries (user_id, (COALESCE(published_at, created_at)) DESC NULLS LAST, id DESC)
  WHERE status <> 'removed';

DROP INDEX IF EXISTS entries_user_id_idx;
DROP INDEX IF EXISTS entries_feed_id_idx;
